package main

// dockmon.go — cliente minimo da Docker Engine API via unix socket.
//
// Serve para o painel de status da UI: detectar se o downloader está rodando,
// iniciar uma nova execução (equivale a `docker compose run --rm downloader`)
// e parar a atual. Sem SDK: HTTP puro sobre o socket. Se o socket não estiver
// disponível (ex.: rootless), a UI degrada para modo somente-leitura.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	dlLabel    = "sank-vault.downloader"
	dlUIName   = "sankvault-downloader-ui"
	apiTimeout = 15 * time.Second
)

var errDownloadActive = errors.New("já existe um download em andamento")

type dockerMon struct {
	sock      string
	hc        *http.Client
	version   string
	avaliavel bool
}

func newDockerMon() *dockerMon {
	m := &dockerMon{sock: env("DOCKER_SOCKET", "/var/run/docker.sock")}
	if _, err := os.Stat(m.sock); err != nil {
		return m
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", m.sock)
		},
	}
	m.hc = &http.Client{Transport: tr, Timeout: apiTimeout}

	// negocia a versão da API pelo header Api-Version do /_ping
	resp, err := m.hc.Get("http://localhost/_ping")
	if err != nil {
		log.Printf("[sank-vault] docker socket em %s indisponivel: %v", m.sock, err)
		return m
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if v := resp.Header.Get("Api-Version"); v != "" {
		m.version = v
	} else {
		m.version = "1.43"
	}
	m.avaliavel = true
	return m
}

func (m *dockerMon) do(method, path string, body any) ([]byte, error) {
	if !m.avaliavel {
		return nil, errors.New("docker socket indisponivel")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://localhost/v"+m.version+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("docker api %s %s: %s (%d)", method, path, bytes.TrimSpace(out), resp.StatusCode)
	}
	return out, nil
}

// dlContainer é um container do downloader encontrado pelo label.
type dlContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

func (m *dockerMon) listDownloaders() ([]dlContainer, error) {
	filters := url.QueryEscape(`{"label":["` + dlLabel + `"]}`)
	out, err := m.do("GET", "/containers/json?all=true&filters="+filters, nil)
	if err != nil {
		return nil, err
	}
	var cs []dlContainer
	return cs, json.Unmarshal(out, &cs)
}

// inspectEnv devolve as envs de um container via inspect.
func (m *dockerMon) inspectEnv(id string) ([]string, error) {
	out, err := m.do("GET", "/containers/"+id+"/json", nil)
	if err != nil {
		return nil, err
	}
	var info struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, err
	}
	return info.Config.Env, nil
}

func (m *dockerMon) startDownloader(mode string) error {
	cs, err := m.listDownloaders()
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.State == "running" {
			return errDownloadActive
		}
	}

	// nome fixo: remove resíduo de execuções anteriores (o AutoRemove cobre a
	// maioria, mas logo após um stop o registro pode existir por instantes)
	_, _ = m.do("DELETE", "/containers/"+dlUIName+"?force=true&link=false", nil)

	payload := map[string]any{
		"Image": env("DOWNLOADER_IMAGE", "sank-vault-downloader:latest"),
		"Env":   []string{"SYNC_MODE=" + mode},
		"Labels": map[string]string{
			dlLabel:                      "1",
			"com.docker.compose.project": env("COMPOSE_PROJECT", "sank-vault"),
			"com.docker.compose.service": "downloader",
		},
		"HostConfig": map[string]any{
			"Binds": []string{
				env("VAULT_MEDIA_VOLUME", "sank-vault_vault_media") + ":/data/media",
				env("VAULT_DB_VOLUME", "sank-vault_vault_db") + ":/data/db",
			},
			"AutoRemove": true,
		},
	}
	out, err := m.do("POST", "/containers/create?name="+dlUIName, payload)
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return err
	}
	_, err = m.do("POST", "/containers/"+created.ID+"/start", nil)
	return err
}

func (m *dockerMon) stopDownloader() error {
	cs, err := m.listDownloaders()
	if err != nil {
		return err
	}
	stoppedAny := false
	for _, c := range cs {
		if c.State != "running" {
			continue
		}
		if _, err := m.do("POST", "/containers/"+c.ID+"/stop?t=10", nil); err != nil {
			return err
		}
		stoppedAny = true
	}
	if !stoppedAny {
		return errors.New("nenhum download ativo")
	}
	return nil
}
