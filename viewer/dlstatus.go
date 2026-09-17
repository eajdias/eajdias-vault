package main

// dlstatus.go — painel do downloader: status + controle via UI.
//
// Leitura: heartbeat do entrypoint.sh em /data/media/.download-status.json.
// Controle (sem socket docker): a UI escreve comandos em /data/db/.uictrl
// (cmd.json p/ start, arquivo stop p/ parada); o autosync executa o start e
// o sampler do entrypoint consome o stop. Respostas em ack.json.
// Ver downloader/autosync.sh (protocolo) — o executor precisa estar no ar.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const statusFileName = ".download-status.json"

type shellStatus struct {
	Running    int    `json:"running"`
	Phase      string `json:"phase"`
	RateBPS    int64  `json:"rate_bps"`
	FilesTotal int64  `json:"files_total"`
	BytesTotal int64  `json:"bytes_total"`
	FilesAdded int64  `json:"files_added"`
	BytesAdded int64  `json:"bytes_added"`
	StartedAt  int64  `json:"started_at"`
	UpdatedAt  int64  `json:"updated_at"`
	LastMoveAt int64  `json:"last_move_at"`
	Mode       string `json:"mode,omitempty"`
}

func readShellStatus(mediaDir string) shellStatus {
	var st shellStatus
	b, err := os.ReadFile(mediaDir + "/" + statusFileName)
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

type dlStatus struct {
	Active       bool   `json:"active"`
	SyncMode     string `json:"sync_mode,omitempty"` // full | incremental
	Phase        string `json:"phase,omitempty"`
	RateBPS      int64  `json:"rate_bps"`
	SessionFiles int64  `json:"session_files"`
	SessionBytes int64  `json:"session_bytes"`
	VaultFiles   int64  `json:"vault_files"`
	VaultBytes   int64  `json:"vault_bytes"`
	UpdatedAt    int64  `json:"updated_at"`
	AgeSeconds   int64  `json:"age_s"`
	Stuck        bool   `json:"stuck,omitempty"`
	Cmd          *dlAck `json:"cmd,omitempty"` // ultimo comando da UI e seu desfecho
	CmdAgeS      int64  `json:"cmd_age_s,omitempty"`
}

const stuckAfterNoProgress = 180 * time.Second

func (s *server) currentDLStatus() dlStatus {
	st := dlStatus{}
	now := time.Now().Unix()

	sh := readShellStatus(s.mediaDir)
	// ativo = heartbeat diz rodando E é recente (<15s)
	hbFresh := sh.UpdatedAt > 0 && now-sh.UpdatedAt < 15
	st.Active = sh.Running == 1 && hbFresh
	if st.Active {
		st.Phase = sh.Phase
		st.SyncMode = sh.Mode
		if st.SyncMode == "" {
			st.SyncMode = "full" // default do entrypoint
		}
	} else if sh.UpdatedAt > 0 {
		st.Phase = sh.Phase // último desfecho: concluido | interrompido
	}

	st.RateBPS = sh.RateBPS
	st.SessionFiles = sh.FilesAdded
	st.SessionBytes = sh.BytesAdded
	st.VaultFiles = sh.FilesTotal
	st.VaultBytes = sh.BytesTotal
	st.UpdatedAt = sh.UpdatedAt
	if sh.LastMoveAt > 0 && st.Active &&
		time.Since(time.Unix(sh.LastMoveAt, 0)) > stuckAfterNoProgress {
		st.Stuck = true
	}

	if sh.UpdatedAt > 0 {
		st.AgeSeconds = now - sh.UpdatedAt
		if st.AgeSeconds < 0 {
			st.AgeSeconds = 0
		}
	}
	if a := s.readAck(); a != nil {
		st.Cmd, st.CmdAgeS = a, now-a.At
		if st.CmdAgeS < 0 {
			st.CmdAgeS = 0
		}
	}
	return st
}

func (s *server) handleDlStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentDLStatus())
}

// ---- controle via UI -------------------------------------------------------

const ctrlDirName = ".uictrl"

var errExecutorDown = errors.New("executor ausente (suba o autosync)")
var errCmdPending = errors.New("ja ha um comando pendente")

type dlCmd struct {
	Action string `json:"action"` // start
	Mode   string `json:"mode,omitempty"`
	ID     int64  `json:"id"`
}

type dlAck struct {
	ID     int64  `json:"id"`
	State  string `json:"state"` // accepted|busy|done|error|stopped|nothing
	Detail string `json:"detail,omitempty"`
	At     int64  `json:"at"`
}

// ctrlDir devolve o dir de controle, ou erro se o executor nunca o criou
// (autosync parado desde o boot = controle indisponivel).
func (s *server) ctrlDir() (string, error) {
	dir := filepath.Join(s.dbDir, ctrlDirName)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", errExecutorDown
	}
	return dir, nil
}

// executorAlive: o autosync toca .uictrl/alive a cada 30s. Sem ele, start
// seria enfileirado e dispararia sozinho no proximo boot — por isso start
// exige executor no ar (503) enquanto stop aceita sempre (qualquer sampler
// consome a flag, e boot futuro cura orfa).
func (s *server) executorAlive() bool {
	fi, err := os.Stat(filepath.Join(s.dbDir, ctrlDirName, "alive"))
	if err != nil || time.Since(fi.ModTime()) > 90*time.Second {
		return false
	}
	return true
}

func (s *server) writeStartCmd(mode string) (int64, error) {
	if !s.executorAlive() {
		return 0, errExecutorDown
	}
	dir, err := s.ctrlDir()
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd.json")); err == nil {
		return 0, errCmdPending
	}
	cmd := dlCmd{Action: "start", Mode: mode, ID: time.Now().Unix()}
	b, _ := json.Marshal(cmd)
	tmp := filepath.Join(dir, "cmd.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "cmd.json")); err != nil {
		return 0, err
	}
	return cmd.ID, nil
}

func (s *server) writeStopFlag() (int64, error) {
	dir, err := s.ctrlDir()
	if err != nil {
		return 0, err
	}
	id := time.Now().Unix()
	// stop e idempotente: sobrescreve sem checar pendencia
	if err := os.WriteFile(filepath.Join(dir, "stop"), []byte(strconv.FormatInt(id, 10)), 0o600); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *server) readAck() *dlAck {
	b, err := os.ReadFile(filepath.Join(s.dbDir, ctrlDirName, "ack.json"))
	if err != nil {
		return nil
	}
	var a dlAck
	if json.Unmarshal(b, &a) != nil || a.ID == 0 {
		return nil
	}
	return &a
}

// handleDlStart agenda um sync: POST {"mode":"full"|"incremental"} (default
// incremental). 202 com o id; o desfecho (accepted/busy/done) chega via
// /api/download/status (campo cmd) em ate ~30s (poll do autosync).
func (s *server) handleDlStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Mode != "full" {
		body.Mode = "incremental" // padrao: pula baixados, para apos 5 seguidos
	}
	id, err := s.writeStartCmd(body.Mode)
	switch {
	case errors.Is(err, errExecutorDown):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, errCmdPending):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "falha ao gravar comando"})
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "mode": body.Mode})
	}
}

// handleDlStop pede parada graciosa do sync ativo (TERM no gallery-dl).
// Idempotente: sem sync ativo o executor responde "nothing".
func (s *server) handleDlStop(w http.ResponseWriter, r *http.Request) {
	id, err := s.writeStopFlag()
	if errors.Is(err, errExecutorDown) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "falha ao gravar parada"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
}

// ---- verificacao (saude + consistencia) ------------------------------------

type dlVerify struct {
	VaultFiles     int64    `json:"vault_files"`
	VaultBytes     int64    `json:"vault_bytes"`
	DiskFreeBytes  int64    `json:"disk_free_bytes"`
	LastPhase      string   `json:"last_phase,omitempty"`
	LastAt         int64    `json:"last_at,omitempty"`
	MediaFiles     int64    `json:"media_files"`
	Sidecars       int64    `json:"sidecars"`
	NoSidecarTotal int64    `json:"media_sem_sidecar_total"`
	OrphansTotal   int64    `json:"sidecars_orfaos_total"`
	NoSidecar      []string `json:"media_sem_sidecar"` // amostra: sem tags (backfill pendente)
	Orphans        []string `json:"sidecars_orfaos"`   // amostra: sidecar sem midia
	Partials       int64    `json:"partials"`          // .part = download interrompido
}

// handleDlVerify: snapshot de saude + consistencia midia<->sidecar.
// Tudo local e sob demanda (2 readdir + mapa em memoria, sem stat por item).
func (s *server) handleDlVerify(w http.ResponseWriter, r *http.Request) {
	v := dlVerify{NoSidecar: []string{}, Orphans: []string{}}
	items, _ := s.rescan()
	media := make(map[string]bool, len(items))
	for _, it := range items {
		media[it.Name] = true
		v.VaultBytes += it.Size
	}
	v.MediaFiles = int64(len(items))
	v.VaultFiles = v.MediaFiles

	side := map[string]bool{}
	collect := func(dir string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if strings.HasPrefix(n, ".") || filepath.Ext(n) != ".json" {
				continue
			}
			side[n[:len(n)-5]] = true
		}
	}
	collect(filepath.Join(s.mediaDir, ".meta"))
	collect(s.mediaDir) // legados da raiz (pre-.meta)
	v.Sidecars = int64(len(side))
	for name := range media {
		if !side[name] {
			v.NoSidecarTotal++
			if len(v.NoSidecar) < 10 {
				v.NoSidecar = append(v.NoSidecar, name)
			}
		}
	}
	for name := range side {
		if !media[name] {
			v.OrphansTotal++
			if len(v.Orphans) < 10 {
				v.Orphans = append(v.Orphans, name+".json")
			}
		}
	}
	if ents, err := os.ReadDir(s.mediaDir); err == nil {
		for _, e := range ents {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".part" {
				v.Partials++
			}
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.mediaDir, &st); err == nil {
		v.DiskFreeBytes = int64(st.Bavail) * st.Bsize
	}
	if sh := readShellStatus(s.mediaDir); sh.UpdatedAt > 0 {
		v.LastPhase, v.LastAt = sh.Phase, sh.UpdatedAt
	}
	writeJSON(w, http.StatusOK, v)
}
