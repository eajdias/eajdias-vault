package main

// durations.go — duração de vídeos via ffprobe com cache JSON persistente.
//
// Os arquivos do vault são imutáveis e o nome já carrega {id}_{md5}, então a
// chave do cache é o próprio nome; mtime/tamanho servem de validação extra.
// O cache mora em /data/thumbs/meta.json (volume rw do viewer).

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// durationMeta é uma entrada do cache. Duration negativo (-1) marca falha
// permanente de probe para aquela versão do arquivo (evita reprovar eterno).
type durationMeta struct {
	Duration float64 `json:"d"`
	MTime    int64   `json:"mt"`
	Size     int64   `json:"sz"`
}

type durationStore struct {
	mu   sync.Mutex
	path string
	data map[string]durationMeta
}

func newDurationStore(path string) *durationStore {
	ds := &durationStore{path: path, data: map[string]durationMeta{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &ds.data)
	}
	return ds
}

// lookup devolve a duração conhecida do arquivo. ok=false significa "precisa
// probar"; ok=true com d<0 significa "probado e falhou" (tratar como sem duração).
func (ds *durationStore) lookup(name string, mtime, size int64) (d float64, ok bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	m, hit := ds.data[name]
	if !hit || m.MTime != mtime || m.Size != size {
		return 0, false
	}
	return m.Duration, true
}

func (ds *durationStore) set(name string, d float64, mtime, size int64) {
	ds.mu.Lock()
	ds.data[name] = durationMeta{Duration: d, MTime: mtime, Size: size}
	ds.mu.Unlock()
}

// prune remove entradas cujos arquivos não existem mais no índice.
func (ds *durationStore) prune(alive map[string]bool) int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	rm := 0
	for k := range ds.data {
		if !alive[k] {
			delete(ds.data, k)
			rm++
		}
	}
	return rm
}

// save grava o cache atomicamente (tmp + rename).
func (ds *durationStore) save() {
	ds.mu.Lock()
	b, err := json.MarshalIndent(ds.data, "", " ")
	ds.mu.Unlock()
	if err != nil {
		return
	}
	tmp := ds.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[sank-vault] meta.json: %v", err)
		return
	}
	_ = os.Rename(tmp, ds.path)
}

// probeDuration roda ffprobe metadata-only no arquivo (sem extensao, tanto faz)
// e devolve a duração do container em segundos.
func probeDuration(path string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"--", path,
	).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(trimSpaceBytes(out), 64)
}

func trimSpaceBytes(b []byte) string {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\n' || b[j-1] == '\r' || b[j-1] == '\t') {
		j--
	}
	return string(b[i:j])
}

// startDurationProber mantém um loop em background que proba vídeos ainda sem
// duração conhecida, 2 por vez, e re-checa de tempos em tempos (novos downloads).
func (s *server) startDurationProber() {
	go func() {
		for {
			s.probePass()
			time.Sleep(30 * time.Second)
		}
	}()
}

// probeStats expõe o progresso atual para /api/stats.
func (s *server) probeStats() (left, total int) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	return s.probeLeft, s.probeTotal
}

func (s *server) probePass() {
	items, paths := s.rescan()

	var targets []Item
	for _, it := range items {
		if it.Kind != "video" {
			continue
		}
		if _, ok := s.durs.lookup(it.Name, it.ModTime, it.Size); !ok {
			targets = append(targets, it)
		}
	}

	s.probeMu.Lock()
	s.probeTotal = len(targets)
	s.probeLeft = len(targets)
	s.probeMu.Unlock()

	if len(targets) == 0 {
		return
	}
	log.Printf("[sank-vault] analisando duração de %d vídeos...", len(targets))

	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, it := range targets {
		wg.Add(1)
		go func(it Item) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			d, err := probeDuration(paths[it.Name])
			if err != nil {
				d = -1 // marca falha; arquivo mudou -> reprova
			}
			s.durs.set(it.Name, d, it.ModTime, it.Size)

			s.probeMu.Lock()
			s.probeLeft--
			done := s.probeLeft
			s.probeMu.Unlock()
			if done%25 == 0 {
				s.durs.save()
			}
		}(it)
	}
	wg.Wait()
	s.durs.save()
	log.Printf("[sank-vault] análise de duração concluída")
}
