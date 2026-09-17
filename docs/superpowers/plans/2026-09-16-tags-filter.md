# Tags + nomes + filtro multi-tag Implementation Plan

> **For agentic workers:** implement this plan task-by-task — dispatch a fresh `general` subagent per task via the task tool, or execute inline with checkpoints. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Vault com tags do Sankaku por mídia, nomes/títulos e tags personalizadas, com pesquisa = filtro AND por N tags + autocomplete vindo do índice local. Validar com 10 mídias em ambiente limpo.

**Architecture:** Downloader grava sidecar `{id}_{md5}.json` com tags via postprocessor `metadata` nativo do gallery-dl (sem código novo no download). Viewer ingere sidecars para um índice central `/data/thumbs/tags.json` (mesmo padrão de `favorites.json`/`meta.json`), filtra em memória e expõe autocomplete por frequência. Sem SQLite (decisão registrada: 1 usuário, sem escrita concorrente; reavaliar só se `tags.json` > ~10MB ou rewrite ficar perceptível).

**Tech Stack:** gallery-dl (postprocessor `metadata` mode `json`), Go 1.22+ stdlib + `golang.org/x/image` (já no `go.mod`; nenhuma dependência nova), JS vanilla (sem harness — verificação via `curl` + checklist de browser).

**Spec:** decisões travadas na conversa (sem doc separado): tags planas no MVP (sem `extractor.sankaku.tags=true`, que custa +1 req/post); filtro AND; autocomplete do índice; `q` por filename aposentado; sidecar em `vault_media` (único volume rw do downloader e ro do viewer) + índice central em `vault_thumbs` (rw do viewer).

## Global Constraints

- `CGO_ENABLED=0` no build do viewer (`viewer/Dockerfile:3`) — nenhuma dependência com CGO.
- Arquivos de mídia ficam SEM extensão; `strip-ext.sh` nunca toca `*.json`.
- `rescan()` tem TTL de 8s (`viewer/main.go:156`) — índice de tags segue o mesmo ciclo, sem goroutine nova.
- Sidecars `*.json` NÃO são mídia: nunca entrar no índice nem no sniff.
- Normalização canônica de tag (usada em ingest, filtro e autocomplete): minúsculo, espaços→`_`, trim. Sankaku já entrega assim; customs do usuário passam pela mesma função.
- Commits convencionais (`feat(viewer): ...`, `feat(downloader): ...`), um por task, só após teste verde.

---

### Task 1: Downloader grava sidecar de tags

**Files:**
- Modify: `downloader/gallery-dl.conf`
- Modify: `downloader/strip-ext.sh`

**Interfaces:**
- Consumes: nada (primeira task).
- Produces: para cada mídia `/data/media/{id}_{md5}` (sem extensão), um sidecar `/data/media/{id}_{md5}.json` contendo ao menos a chave `tags` (lista) ou `tag_names` (fallback). Task 2 consome esse contrato.

- [ ] **Step 1: Editar `gallery-dl.conf` adicionando o postprocessor `metadata`**

Conteúdo final exato do arquivo:

```json
{
    "base-directory": "/data/media/",
    "directory": [],
    "filename": {
        "md5": "{id}_{md5}",
        "":   "{id}_{num}"
    },
    "retries": 10,
    "http-timeout": 60,
    "postprocessors": [
        {
            "name": "metadata",
            "mode": "json",
            "event": "post",
            "filename": "{id}_{md5}.json"
        },
        {
            "name": "exec",
            "event": "after",
            "async": false,
            "command": ["/app/strip-ext.sh", "{}"]
        }
    ]
}
```

Ordem importa: `metadata` antes do `exec` para o sidecar existir antes do strip (o strip só recebe a mídia via `{}`, mas a ordem deixa a intenção explícita).

- [ ] **Step 2: Travar `strip-ext.sh` contra `*.json`**

Conteúdo final exato:

```sh
#!/bin/sh
# Remove a extensao final de cada arquivo recebido como argumento.
# Usado como postprocessor "exec" do gallery-dl para ocultar o tipo da midia.
# Sidecars *.json (tags) nunca sao tocados.
for f in "$@"; do
	[ -f "$f" ] || continue
	case "$f" in
	*.json) continue ;;
	*.*) mv -f -- "$f" "${f%.*}" ;;
	esac
done
```

- [ ] **Step 3: Rebuild + download de teste com 3 arquivos**

Run: `docker compose build downloader && docker compose run --rm -e GDL_EXTRA_ARGS="--range 1-3 --sleep-request 1" downloader`
Expected: exit 0, log mostra `tag=fav:zubirous modo=full`.

- [ ] **Step 4: Verificar sidecar ao lado da mídia**

Run: `docker compose run --rm --entrypoint sh downloader -c 'ls /data/media | head -10; echo ---; cat /data/media/$(ls /data/media | grep -v "^\." | head -1).json | head -c 600'`
Expected: lista mostra arquivos sem extensão E arquivos `{id}_{md5}.json`; o `cat` mostra JSON com `"tags": [...]` (ou `"tag_names"`). Se o sidecar tiver outro nome/formato, ajustar o `filename` do Step 1 e repetir Step 3 (não seguir para Task 2 sem o contrato cumprido).

- [ ] **Step 5: Commit**

```bash
git add downloader/gallery-dl.conf downloader/strip-ext.sh
git commit -m "feat(downloader): grava sidecar de tags {id}_{md5}.json"
```

---

### Task 2: Viewer — `tagStore` + ingestão no `rescan`

**Files:**
- Create: `viewer/tags.go`
- Create: `viewer/tags_test.go`
- Modify: `viewer/main.go:36-44` (struct `Item`), `viewer/main.go:165-214` (`rescan`)

**Interfaces:**
- Consumes: sidecar `/data/media/{id}_{md5}.json` com `tags` ou `tag_names` (Task 1).
- Produces (usado pelas Tasks 3–4 com estes nomes exatos):
  - `func normTag(s string) string`
  - `type tagEntry struct { Tags []string; Custom []string; Title string }` (JSON: `tags`, `custom,omitempty`, `title,omitempty`)
  - `type tagStore struct` + `func newTagStore(path string) *tagStore` (carrega `/data/thumbs/tags.json` formato `map[nome]*tagEntry`)
  - `func (t *tagStore) ingest(name string, tags []string)` (cria entrada se ausente; nunca sobrescreve `Custom`/`Title`)
  - `func (t *tagStore) freq() map[string]int` (conta tags+customs, chaves já normalizadas)
  - `func (t *tagStore) save()` (tmp+rename atômico, igual `favStore.toggle`)
  - `func (t *tagStore) get(name string) *tagEntry` (cópia; nil se ausente)
  - `server` ganha campo `tags *tagStore`, inicializado no `main()` com `filepath.Join(thumbDir, "tags.json")`
  - `Item` ganha `Tags []string json:"tags,omitempty"`, `Custom []string json:"custom,omitempty"`, `Title string json:"title,omitempty"`

- [ ] **Step 1: Escrever `viewer/tags_test.go` (falha primeiro)**

```go
package main

import (
	"path/filepath"
	"testing"
)

func TestNormTag(t *testing.T) {
	cases := map[string]string{
		"Blue Archive":  "blue_archive",
		"  LONG_hair ":  "long_hair",
		"1GIRL":         "1girl",
		"school_swimsuit": "school_swimsuit",
	}
	for in, want := range cases {
		if got := normTag(in); got != want {
			t.Errorf("normTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTagStoreIngestPreservesCustom(t *testing.T) {
	ts := newTagStore(filepath.Join(t.TempDir(), "tags.json"))
	ts.ingest("1_abc", []string{"1girl", "Long Hair"})
	ts.data["1_abc"].Custom = []string{"minha_tag"}
	ts.data["1_abc"].Title = "Meu nome"
	ts.ingest("1_abc", []string{"1girl", "long_hair", "new_tag"})
	e := ts.data["1_abc"]
	if len(e.Custom) != 1 || e.Custom[0] != "minha_tag" {
		t.Fatalf("custom perdido: %+v", e)
	}
	if e.Title != "Meu nome" {
		t.Fatalf("title perdido: %+v", e)
	}
	found := map[string]bool{}
	for _, tg := range e.Tags {
		found[tg] = true
	}
	if !found["new_tag"] || !found["long_hair"] {
		t.Fatalf("tags fonte nao mescladas: %+v", e.Tags)
	}
}

func TestTagStoreFreq(t *testing.T) {
	ts := newTagStore(filepath.Join(t.TempDir(), "tags.json"))
	ts.ingest("1_a", []string{"1girl", "solo"})
	ts.ingest("2_b", []string{"1girl"})
	ts.data["2_b"].Custom = []string{"solo"}
	f := ts.freq()
	if f["1girl"] != 2 || f["solo"] != 2 {
		t.Fatalf("freq errada: %+v", f)
	}
}

func TestTagStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	ts := newTagStore(filepath.Join(dir, "tags.json"))
	ts.ingest("1_a", []string{"1girl"})
	ts.data["1_a"].Title = "X"
	ts.save()
	ts2 := newTagStore(filepath.Join(dir, "tags.json"))
	e := ts2.get("1_a")
	if e == nil || e.Title != "X" || len(e.Tags) != 1 || e.Tags[0] != "1girl" {
		t.Fatalf("reload falhou: %+v", e)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./...` (em `viewer/`)
Expected: FAIL (`undefined: normTag`, etc.)

- [ ] **Step 3: Escrever `viewer/tags.go` (implementação mínima)**

```go
package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
)

// tagEntry é o registro de uma mídia no índice de tags.
// Tags vem da fonte (Sankaku, via sidecar); Custom/Title são do usuário
// e nunca são sobrescritos pela ingestão.
type tagEntry struct {
	Tags   []string `json:"tags"`
	Custom []string `json:"custom,omitempty"`
	Title  string   `json:"title,omitempty"`
}

// normTag é a normalização canônica: minúsculo, espaços→"_", trim.
func normTag(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_")
}

type tagStore struct {
	mu   sync.Mutex
	path string
	data map[string]*tagEntry
}

func newTagStore(path string) *tagStore {
	ts := &tagStore{path: path, data: map[string]*tagEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &ts.data)
		if ts.data == nil {
			ts.data = map[string]*tagEntry{}
		}
		for _, e := range ts.data {
			for i, tg := range e.Tags {
				e.Tags[i] = normTag(tg)
			}
			for i, tg := range e.Custom {
				e.Custom[i] = normTag(tg)
			}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[tags] não consegui ler %s: %v", path, err)
	}
	return ts
}

// ingest cria a entrada se ausente e mescla tags da fonte (deduplicadas,
// normalizadas). Custom/Title existentes são preservados.
func (t *tagStore) ingest(name string, tags []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.data[name]
	if !ok {
		e = &tagEntry{}
		t.data[name] = e
	}
	seen := map[string]bool{}
	for _, tg := range e.Tags {
		seen[tg] = true
	}
	for _, tg := range tags {
		tg = normTag(tg)
		if tg == "" || seen[tg] {
			continue
		}
		seen[tg] = true
		e.Tags = append(e.Tags, tg)
	}
}

// get devolve cópia da entrada (nil se ausente).
func (t *tagStore) get(name string) *tagEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.data[name]
	if !ok {
		return nil
	}
	cp := &tagEntry{Title: e.Title}
	cp.Tags = append([]string{}, e.Tags...)
	cp.Custom = append([]string{}, e.Custom...)
	return cp
}

// freq conta ocorrências de cada tag (fonte+custom) para autocomplete.
func (t *tagStore) freq() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := map[string]int{}
	for _, e := range t.data {
		for _, tg := range e.Tags {
			f[tg]++
		}
		for _, tg := range e.Custom {
			f[tg]++
		}
	}
	return f
}

// save persiste atomicamente (tmp + rename).
func (t *tagStore) save() {
	t.mu.Lock()
	b, err := json.Marshal(t.data)
	t.mu.Unlock()
	if err != nil {
		log.Printf("[tags] marshal: %v", err)
		return
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[tags] write: %v", err)
		return
	}
	_ = os.Rename(tmp, t.path)
}

// prune remove entradas de mídias que não existem mais.
func (t *tagStore) prune(alive map[string]bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	rm := false
	for k := range t.data {
		if !alive[k] {
			delete(t.data, k)
			rm = true
		}
	}
	return rm
}
```

- [ ] **Step 4: Ligar no `main.go` — 4 edições exatas**

  a) Struct `Item` (`main.go:36-44`): adicionar após `Fav`:
```go
	Tags     []string `json:"tags,omitempty"`
	Custom   []string `json:"custom,omitempty"`
	Title    string   `json:"title,omitempty"`
```

  b) Struct `server` (após campo `favs`): adicionar `tags *tagStore`.

  c) Construtor no `main()` (após `favs: newFavStore(...)`): adicionar `tags: newTagStore(filepath.Join(thumbDir, "tags.json")),`.

  d) `rescan()`: pular sidecars no `WalkDir` (na condição existente de `main.go:170`, trocar por):
```go
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".json") {
			return nil // ocultos, incompletos e sidecars de tags
		}
```
E no loop de enriquecimento (após `it.Fav = s.favs.has(it.Name)` em `main.go:203`), inserir:
```go
		if e := s.tags.get(it.Name); e != nil {
			it.Tags, it.Custom, it.Title = e.Tags, e.Custom, e.Title
		} else if raw, err := os.ReadFile(filepath.Join(s.mediaDir, it.Name+".json")); err == nil {
			var side map[string]any
			if json.Unmarshal(raw, &side) == nil {
				var tg []string
				for _, k := range []string{"tags", "tag_names"} {
					if v, ok := side[k].([]any); ok {
						for _, x := range v {
							if s, ok := x.(string); ok {
								tg = append(tg, s)
							}
						}
						break
					}
				}
				s.tags.ingest(it.Name, tg)
				if e := s.tags.get(it.Name); e != nil {
					it.Tags, it.Custom, it.Title = e.Tags, e.Custom, e.Title
				}
			}
		}
```
E após `s.durs.prune(alive)` (`main.go:206`), inserir:
```go
	if s.tags.prune(alive) {
		s.tags.save()
	} else {
		s.tags.save() // persiste ingestões novas do passe atual
	}
```
Simplificação válida: chamar `s.tags.save()` incondicionalmente após o prune (o mapa só cresce por ingest; save de poucos KB é barato). Usar forma simples:
```go
	s.tags.prune(alive)
	s.tags.save()
```

- [ ] **Step 5: Rodar testes + build**

Run: `go test ./... && go vet ./... && go build ./...` (em `viewer/`)
Expected: PASS nos 4 testes novos + nenhum teste antigo quebrado (não há outros), vet limpo.

- [ ] **Step 6: Commit**

```bash
git add viewer/tags.go viewer/tags_test.go viewer/main.go
git commit -m "feat(viewer): indice de tags com ingestao de sidecars"
```

---

### Task 3: Filtro AND por N tags + autocomplete

**Files:**
- Modify: `viewer/main.go:295-346` (`handleItems`), `viewer/main.go:129-142` (rotas)
- Create: `viewer/filter_test.go`

**Interfaces:**
- Consumes: `normTag`, `tagStore.get`, `tagStore.freq` (Task 2); `Item.Tags/Custom/Title`.
- Produces (contrato HTTP usado pela Task 6):
  - `GET /api/items?tags=a,b,c&...` — vírgula separa tags (AND sobre união tags+custom, normalizadas); `tags` vazio = sem filtro. Parâmetro `q` removido (aposenta busca por filename). Demais params (`category/sort/seed/offset/limit`) inalterados.
  - `GET /api/tags?prefix=&limit=20` → `{"tags":[{"tag":"1girl","count":3}]}` ordenado por count desc, empate alfabético; `limit` default 20, max 100.

- [ ] **Step 1: Escrever `viewer/filter_test.go` (falha primeiro)**

```go
package main

import "testing"

func matchTagsForTest(it Item, want []string) bool {
	return matchTags(it, want)
}

func TestMatchTagsAND(t *testing.T) {
	it := Item{Name: "1_a", Tags: []string{"1girl", "solo"}, Custom: []string{"minha_tag"}}
	if !matchTagsForTest(it, []string{}) {
		t.Error("filtro vazio deve casar tudo")
	}
	if !matchTagsForTest(it, []string{"1girl", "solo"}) {
		t.Error("AND com 2 tags da fonte deve casar")
	}
	if !matchTagsForTest(it, []string{"1GIRL", "Minha Tag"}) {
		t.Error("match deve normalizar")
	}
	if !matchTagsForTest(it, []string{"solo", "minha_tag"}) {
		t.Error("fonte+custom devem unir")
	}
	if matchTagsForTest(it, []string{"1girl", "inexistente"}) {
		t.Error("AND com tag ausente nao deve casar")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run TestMatchTags ./...` (em `viewer/`)
Expected: FAIL (`undefined: matchTags`).

- [ ] **Step 3: Implementar `matchTags` + trocar `q` por `tags` no `handleItems`**

Adicionar a `main.go` (junto de `catOf`):

```go
// matchTags: AND sobre a união de Tags+Custom, tudo normalizado.
// want vazio casa com tudo.
func matchTags(it Item, want []string) bool {
	if len(want) == 0 {
		return true
	}
	have := make(map[string]bool, len(it.Tags)+len(it.Custom))
	for _, tg := range it.Tags {
		have[normTag(tg)] = true
	}
	for _, tg := range it.Custom {
		have[normTag(tg)] = true
	}
	for _, w := range want {
		if !have[normTag(w)] {
			return false
		}
	}
	return true
}
```

Em `handleItems`, substituir as 2 linhas do `q`:
```go
	q := strings.ToLower(strings.TrimSpace(r.FormValue("q")))
```
por:
```go
	var wantTags []string
	for _, tg := range strings.Split(r.FormValue("tags"), ",") {
		if tg = normTag(tg); tg != "" {
			wantTags = append(wantTags, tg)
		}
	}
```
E no loop, substituir:
```go
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) {
			continue
		}
```
por:
```go
		if !matchTags(it, wantTags) {
			continue
		}
```

- [ ] **Step 4: Adicionar `GET /api/tags` (autocomplete por frequência)**

Rota em `main()` junto das demais:
```go
	mux.HandleFunc("GET /api/tags", srv.requireAuth(srv.handleTags))
```

Handler (após `handleStats`):

```go
// handleTags serve autocomplete: prefixo sobre tags conhecidas (fonte+custom),
// ordenado por frequência desc (empate: alfabético).
func (s *server) handleTags(w http.ResponseWriter, r *http.Request) {
	prefix := normTag(r.FormValue("prefix"))
	limit, _ := strconv.Atoi(r.FormValue("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	type tc struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	out := []tc{}
	for tg, c := range s.tags.freq() {
		if prefix != "" && !strings.HasPrefix(tg, prefix) {
			continue
		}
		out = append(out, tc{tg, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []tc{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": out})
}
```

`strconv`, `sort`, `strings` já importados em `main.go`.

- [ ] **Step 5: Rodar testes + build**

Run: `go test ./... && go vet ./... && go build ./...` (em `viewer/`)
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add viewer/main.go viewer/filter_test.go
git commit -m "feat(viewer): filtro AND por N tags e autocomplete /api/tags"
```

---

### Task 4: Tags personalizadas + título por mídia

**Files:**
- Modify: `viewer/tags.go` (métodos de escrita), `viewer/main.go` (rotas+handlers)
- Modify: `viewer/tags_test.go` (append de testes)

**Interfaces:**
- Consumes: `tagStore` (Task 2).
- Produces (contrato HTTP usado pela Task 6):
  - `POST /api/ctag/{name}` body `{"add":["a"],"del":["b"]}` → `{"tags":[],"custom":[],"title":""}` (estado final da entrada). `add`/`del` normalizados; desconhecidas em `del` ignoradas; entrada criada se ausente.
  - `POST /api/title/{name}` body `{"title":"..."}` → mesmo retorno; título vazio limpa (volta a exibir `id_md5`).
  - Ambos 404 se a mídia não existir no índice. Ambos refletem no cache `s.items` em memória (igual `handleFavToggle` faz com `Fav`).

- [ ] **Step 1: Append testes em `viewer/tags_test.go`**

```go
func TestTagStoreCustomAddDel(t *testing.T) {
	ts := newTagStore(filepath.Join(t.TempDir(), "tags.json"))
	ts.ingest("1_a", []string{"1girl"})
	got := ts.editCustom("1_a", []string{"Minha Tag", "1girl"}, nil)
	if len(got.Custom) != 2 {
		t.Fatalf("add deve unir sem duplicar: %+v", got.Custom)
	}
	got = ts.editCustom("1_a", nil, []string{"minha_tag", "fantasma"})
	if len(got.Custom) != 1 || got.Custom[0] != "1girl" {
		t.Fatalf("del falhou: %+v", got.Custom)
	}
}

func TestTagStoreTitle(t *testing.T) {
	ts := newTagStore(filepath.Join(t.TempDir(), "tags.json"))
	ts.ingest("1_a", []string{"1girl"})
	if got := ts.setTitle("1_a", "  Meu Nome  "); got.Title != "  Meu Nome  " {
		t.Fatalf("title deve guardar verbatim: %q", got.Title)
	}
	if got := ts.setTitle("1_a", ""); got.Title != "" {
		t.Fatalf("title vazio deve limpar: %q", got.Title)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run 'TestTagStoreCustom|TestTagStoreTitle' ./...` (em `viewer/`)
Expected: FAIL (`undefined: editCustom/setTitle`).

- [ ] **Step 3: Implementar em `viewer/tags.go`**

```go
// editCustom adiciona/remove customs (normalizados, sem duplicatas),
// cria a entrada se ausente, persiste e devolve cópia do estado final.
func (t *tagStore) editCustom(name string, add, del []string) *tagEntry {
	t.mu.Lock()
	e, ok := t.data[name]
	if !ok {
		e = &tagEntry{}
		t.data[name] = e
	}
	drop := map[string]bool{}
	for _, tg := range del {
		if tg = normTag(tg); tg != "" {
			drop[tg] = true
		}
	}
	kept := e.Custom[:0]
	seen := map[string]bool{}
	for _, tg := range e.Custom {
		if !drop[tg] && !seen[tg] {
			seen[tg] = true
			kept = append(kept, tg)
		}
	}
	for _, tg := range add {
		if tg = normTag(tg); tg != "" && !seen[tg] {
			seen[tg] = true
			kept = append(kept, tg)
		}
	}
	e.Custom = kept
	cp := &tagEntry{Title: e.Title}
	cp.Tags = append([]string{}, e.Tags...)
	cp.Custom = append([]string{}, e.Custom...)
	t.mu.Unlock()
	t.save()
	return cp
}

// setTitle define (verbatim, com espaços do usuário) ou limpa o título.
func (t *tagStore) setTitle(name, title string) *tagEntry {
	t.mu.Lock()
	e, ok := t.data[name]
	if !ok {
		e = &tagEntry{}
		t.data[name] = e
	}
	e.Title = title
	cp := &tagEntry{Title: e.Title}
	cp.Tags = append([]string{}, e.Tags...)
	cp.Custom = append([]string{}, e.Custom...)
	t.mu.Unlock()
	t.save()
	return cp
}
```

- [ ] **Step 4: Handlers + rotas em `viewer/main.go`**

Rotas (junto das demais):
```go
	mux.HandleFunc("POST /api/ctag/{name}", srv.requireAuth(srv.handleCTag))
	mux.HandleFunc("POST /api/title/{name}", srv.requireAuth(srv.handleTitle))
```

Handlers (após `handleFavToggle`):

```go
// handleCTag edita customs: body {"add":[],"del":[]}. 404 se mídia inexistente.
func (s *server) handleCTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, paths := s.rescan(); func() bool { _, ok := paths[name]; return !ok }() {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Add []string `json:"add"`
		Del []string `json:"del"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	e := s.tags.editCustom(name, body.Add, body.Del)
	s.mu.Lock()
	for i := range s.items {
		if s.items[i].Name == name {
			s.items[i].Custom = e.Custom
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, e)
}

// handleTitle define/limpa o título: body {"title":"..."}. 404 se inexistente.
func (s *server) handleTitle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, paths := s.rescan(); func() bool { _, ok := paths[name]; return !ok }() {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	e := s.tags.setTitle(name, body.Title)
	s.mu.Lock()
	for i := range s.items {
		if s.items[i].Name == name {
			s.items[i].Title = e.Title
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, e)
}
```

- [ ] **Step 5: Rodar testes + build**

Run: `go test ./... && go vet ./... && go build ./...` (em `viewer/`)
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add viewer/tags.go viewer/tags_test.go viewer/main.go
git commit -m "feat(viewer): tags personalizadas e titulo por midia"
```

---

### Task 5: Backfill de tags do acervo já baixado

**Files:**
- Create: `downloader/backfill-tags.sh`

**Interfaces:**
- Consumes: filenames `{id}_{md5}` em `/data/media`; credenciais `SANKAKU_USER/SANKAKU_PASS` (mesmos defaults do `entrypoint.sh`).
- Produces: sidecars `{id}_{md5}.json` no contrato da Task 1 (Task 2 os ingere; já testado com downloads novos na Task 1).

- [ ] **Step 1: Confirmar a flag de dump do gallery-dl na imagem real**

Run: `docker compose run --rm --entrypoint sh downloader -c 'gallery-dl --help 2>&1 | grep -B1 -A2 "dump-json"'`
Expected: linha com `--dump-json` (curta `-j`). Se o nome for outro, usar o nome real nos steps abaixo.

- [ ] **Step 2: Validar extração de tags de 1 post real**

Run (troque `IDEXEMPLO` por um id real: pegue do nome de um arquivo já baixado, parte antes do `_`):
`docker compose run --rm --entrypoint sh downloader -c 'gallery-dl -j -u "$SANKAKU_USER" -p "$SANKAKU_PASS" "https://sankaku.app/posts/show/IDEXEMPLO" 2>/dev/null | head -c 800'`
Expected: JSON contendo `"tags": [...]` ou `"tag_names": [...]`. O script do Step 3 aceita ambas as chaves (primeira que existir).

- [ ] **Step 3: Criar `downloader/backfill-tags.sh`**

```sh
#!/bin/sh
# Backfill one-shot: para cada midia /data/media/{id}_{md5} sem sidecar,
# busca os metadados do post e grava {id}_{md5}.json com as tags.
# Idempotente: pula quem ja tem sidecar. Throttle de 2s entre posts.
set -u
SANKAKU_USER="${SANKAKU_USER:-zubirous@gmail.com}"
SANKAKU_PASS="${SANKAKU_PASS:-Zumbirou\$026}"
MEDIA_DIR=/data/media
LIMIT="${1:-0}"
n=0
for f in "$MEDIA_DIR"/*; do
	[ -f "$f" ] || continue
	name=$(basename "$f")
	case "$name" in .*|*.json|*.part) continue ;; esac
	[ -f "$f.json" ] && continue
	id=${name%%_*}
	[ -n "$id" ] || continue
	if gallery-dl -j -u "$SANKAKU_USER" -p "$SANKAKU_PASS" \
		"https://sankaku.app/posts/show/$id" 2>/dev/null \
		| python3 -c 'import json,sys; d=json.loads(sys.stdin.read()); ts=d.get("tags") or d.get("tag_names") or []; print(json.dumps({"tags":ts}))' >"$f.json.tmp"; then
		mv "$f.json.tmp" "$f.json"
		n=$((n + 1))
		echo "[$n] $name ok"
	else
		rm -f "$f.json.tmp"
		echo "[$n] $name FALHOU (mantido p/ retry)" >&2
	fi
	sleep 2
	if [ "$LIMIT" -gt 0 ] && [ "$n" -ge "$LIMIT" ]; then break; fi
done
echo "backfill concluido: $n sidecars"
```

`chmod +x` via `Dockerfile` (adicionar `/app/backfill-tags.sh` ao `COPY`+`chmod` existentes) ou `chmod +x` local antes do build — o `COPY` preserva o bit. Fazer `chmod +x downloader/backfill-tags.sh` no host e adicionar a linha ao `COPY` do Dockerfile.

- [ ] **Step 4: Testar com 2 mídias (ambiente limpo tem ~10)**

Run: `docker compose build downloader && docker compose run --rm --entrypoint /app/backfill-tags.sh downloader 2`
Expected: `backfill concluido: 2 sidecars`, 2 arquivos `*.json` novos, `cat` de um deles mostra `"tags":[...]`. Rodar de novo com mesmo limite → pula os 2 (idem-potência) e completa mais 2.

- [ ] **Step 5: Commit**

```bash
git add downloader/backfill-tags.sh downloader/Dockerfile
git commit -m "feat(downloader): backfill one-shot de tags via dump-json"
```

---

### Task 6: UI — barra de filtro multi-tag + customs no lightbox

**Files:**
- Modify: `viewer/static/index.html`, `viewer/static/app.js`, `viewer/static/style.css`
- Modify: `README.md` (seção de tags)

**Interfaces:**
- Consome os contratos HTTP das Tasks 3–4 (nomes exatos). Nenhuma interface nova.

- [ ] **Step 1: `index.html` — barra de filtro acima das abas**

Inserir após `</header>`, antes de `<nav id="tabs">`:
```html
<div id="tagbar">
  <div id="chips"></div>
  <input id="taginput" type="search" placeholder="filtrar por tags… (Enter adiciona)" autocomplete="off" aria-label="Filtrar por tags">
  <ul id="suggest" hidden></ul>
</div>
```

- [ ] **Step 2: `app.js` — estado, fetch com `tags`, autocomplete e chips**

Comportamento exato:
  - `state.tags = []` (lista de tags normalizadas locais só p/ exibição; normalização real no servidor).
  - `loadMore()` adiciona `tags: state.tags.join(",")` ao `URLSearchParams` e `syncUrl()` reflete em `?tags=a+b` (usa `URLSearchParams.set("tags", ...)` com `+` automático por encoding de espaço→`+`; vírgula vai encoded, servidor faz split por `,` — ok).
  - `input#taginput` evento `input`: debounce 150ms → `GET /api/tags?prefix=<valor>&limit=8` → renderiza `ul#suggest` (texto `tag (count)`); Enter ou click adiciona chip (se vazio e há sugestão, usa a 1ª).
  - Chips com `×` removem; qualquer mudança → `reset()`.
  - Cards mostram até 3 tags clicáveis (click = adiciona ao filtro); lightbox mostra todas + customs com estilo diferente + título (ou nome do arquivo se sem título).
  - Lightbox: input de custom (`Enter` = `POST /api/ctag/{name}` `{"add":[...]}`), `×` em custom existente = `{"del":[...]}`; input de título + salvar = `POST /api/title/{name}`.
  - Na inicialização, ler `?tags=` da URL para `state.tags` (deep-link) e renderizar chips.

- [ ] **Step 3: `style.css` — estilos mínimos (`#tagbar`, `#chips .tchip`, `#suggest`, `.ttag`, `.tcustom`)**

Reaproveitar variáveis/cores existentes (chip/star). Sem framework, sem animação nova.

- [ ] **Step 4: Verificação ponta a ponta com o viewer real**

```bash
docker compose up -d --build viewer
curl -s -c /tmp/cj -b /tmp/cj -X POST --data 'password=Sankvault@123' http://127.0.0.1:8090/login -o /dev/null
curl -s -b /tmp/cj 'http://127.0.0.1:8090/api/items?tags=&limit=60' | head -c 400
curl -s -b /tmp/cj 'http://127.0.0.1:8090/api/tags?limit=5'
```
Expected: items com `tags` preenchidas (ingeridas dos sidecars do teste da Task 1/5); `/api/tags` lista `{tag,count}`. Depois checklist no browser (`http://127.0.0.1:8090`): digitar prefixo → sugestões; Enter → chip + grid filtra; 2ª tag → AND estreita; click em tag do card adiciona; lightbox edita custom+título e persiste após reload; `?tags=` direto na URL restaura o filtro.

- [ ] **Step 5: README — documentar (append na seção de Abas/Endpoints)**

Incluir: sidecar `{id}_{md5}.json`, backfill (`docker compose run --rm --entrypoint /app/backfill-tags.sh downloader [N]`), endpoints novos (`tags`, `/api/tags`, `/api/ctag/`, `/api/title/`), semântica AND, normalização, gatilho de reavaliação do SQLite (`tags.json` > ~10MB).

- [ ] **Step 6: Commit**

```bash
git add viewer/static/app.js viewer/static/index.html viewer/static/style.css README.md
git commit -m "feat(ui): filtro multi-tag com autocomplete e customs"
```

---

## Self-review (executado na escrita)

1. **Cobertura:** tags fonte (T1+T2+T5 backfill) ✓; nomes/títulos (T4+T6 lightbox) ✓; customs (T4+T6) ✓; filtro N tags AND (T3+T6) ✓; autocomplete do índice (T3+T6) ✓; `q` aposentado (T3) ✓; sidecar ignorado no índice (T2-d) ✓; decisão SQLite documentada no plano + README (T6-5) ✓.
2. **Placeholders:** nenhum `TBD/TODO`; flag `--dump-json` validada em T5-1/2 antes do script; formato do sidecar validado em T1-4 antes do ingest.
3. **Consistência:** `normTag/ingest/freq/save/get/editCustom/setTitle` definidos em T2–T4 com assinaturas idênticas nos testes e na implementação; contratos HTTP (`/api/items?tags=`, `/api/tags`, `/api/ctag/`, `/api/title/`) iguais em T3–T4 e T6; campo `server.tags` + `Item.Tags/Custom/Title` criados em T2 e usados em T3–T4.
