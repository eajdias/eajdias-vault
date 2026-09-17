package main

// dlstatus.go — painel de status do downloader (somente leitura).
//
// Lê o heartbeat escrito pelo entrypoint.sh em /data/media/.download-status.json
// (fase, taxa, bytes/arquivos da sessão). Downloads nunca são disparados pela
// UI: início e parada acontecem pelo terminal ou pelo autosync.

import (
	"encoding/json"
	"net/http"
	"os"
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
	return st
}

func (s *server) handleDlStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentDLStatus())
}
