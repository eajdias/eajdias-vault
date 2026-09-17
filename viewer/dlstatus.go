package main

// dlstatus.go — painel de download da UI.
//
// Mescla duas fontes:
//   1. heartbeat escrito pelo entrypoint.sh em /data/media/.download-status.json
//      (fase, taxa, bytes/arquivos da sessão — sobrevive mesmo sem Docker API);
//   2. estado real do container do downloader via Docker Engine API (rodando?,
//      iniciado pela UI ou pelo terminal, modo de sync).
//
// Se o socket do Docker não estiver disponível, docker_ok=false e a UI esconde
// os botões de controle (somente-leitura).

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
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
	DockerOK     bool   `json:"docker_ok"`
	Active       bool   `json:"active"`
	Source       string `json:"source,omitempty"`          // ui | cli
	State        string `json:"container_state,omitempty"` // running | exited | ...
	SyncMode     string `json:"sync_mode,omitempty"`       // full | incremental
	Phase        string `json:"phase,omitempty"`
	RateBPS      int64  `json:"rate_bps"`
	SessionFiles int64  `json:"session_files"`
	SessionBytes int64  `json:"session_bytes"`
	VaultFiles   int64  `json:"vault_files"`
	VaultBytes   int64  `json:"vault_bytes"`
	UpdatedAt    int64  `json:"updated_at"`
	AgeSeconds   int64  `json:"age_s"`
	Stuck        bool   `json:"stuck,omitempty"`
}

const stuckAfterNoProgress = 180 * time.Second

func (s *server) currentDLStatus() dlStatus {
	st := dlStatus{DockerOK: s.docker.avaliavel}
	now := time.Now().Unix()

	if s.docker.avaliavel {
		cs, err := s.docker.listDownloaders()
		if err == nil {
			for _, c := range cs {
				if c.State != "running" {
					continue
				}
				st.Active = true
				st.State = c.State
				if _, viaUI := c.Labels[dlLabel]; viaUI {
					st.Source = "ui"
				} else {
					st.Source = "cli"
				}
				envs, _ := s.docker.inspectEnv(c.ID)
				for _, e := range envs {
					if strings.HasPrefix(e, "SYNC_MODE=") {
						st.SyncMode = e[len("SYNC_MODE="):]
					}
				}
				break
			}
		}
	}

	sh := readShellStatus(s.mediaDir)
	// ativo se o container roda OU o heartbeat é recente (<15s)
	hbFresh := sh.UpdatedAt > 0 && time.Now().Unix()-sh.UpdatedAt < 15
	if !st.Active && hbFresh {
		st.Active = true
	}
	if st.Active {
		if sh.Running == 1 {
			st.Phase = sh.Phase
			st.SyncMode = sh.Mode
			if st.SyncMode == "" {
				st.SyncMode = "full" // default do entrypoint
			}
		} else if st.Phase == "" {
			st.Phase = "baixando"
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
	return st
}

func (s *server) handleDlStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentDLStatus())
}

func (s *server) handleDlStart(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != "incremental" && mode != "full" {
		mode = "full"
	}
	if err := s.docker.startDownloader(mode); err != nil {
		if errors.Is(err, errDownloadActive) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.currentDLStatus())
}

func (s *server) handleDlStop(w http.ResponseWriter, r *http.Request) {
	if err := s.docker.stopDownloader(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.currentDLStatus())
}
