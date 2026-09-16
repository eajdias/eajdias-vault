package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFS embed.FS

var (
	indexHTML []byte
	appJS     []byte
	styleCSS  []byte
	assetVer  string
)

// Item descreve uma midia do vault para a API/UI.
type Item struct {
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	ModTime  int64    `json:"mtime"`
	MIME     string   `json:"mime"`
	Kind     string   `json:"kind"`               // image | video | other
	Duration float64  `json:"duration,omitempty"` // segundos (apenas videos)
	Fav      bool     `json:"fav,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Custom   []string `json:"custom,omitempty"`
	Title    string   `json:"title,omitempty"`
}

type server struct {
	mediaDir string
	thumbDir string
	password string
	secret   []byte

	mu        sync.Mutex
	items     []Item
	paths     map[string]string // nome -> caminho completo
	indexedAt time.Time

	durs   *durationStore
	docker *dockerMon
	favs   *favStore
	tags   *tagStore

	sem chan struct{} // limite de thumbs geradas em paralelo (ffmpeg/decode)

	fmu   sync.Mutex
	fails map[string]*failRec // rate limit de login por IP

	probeMu    sync.Mutex
	probeLeft  int // videos na fila de analise de duracao
	probeTotal int

	tmu  sync.Mutex
	busy map[string]bool // geracao de thumb em andamento
}

// noindex: exposto via dominio publico (cloudflared), nao deve ser indexado
func noindex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("User-agent: *\nDisallow: /\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	indexHTML, _ = fs.ReadFile(staticFS, "static/index.html")
	appJS, _ = fs.ReadFile(staticFS, "static/app.js")
	styleCSS, _ = fs.ReadFile(staticFS, "static/style.css")

	// Versão dos assets: hash curto do JS+CSS injetado nas URLs (?v=...).
	// Qualquer mudança no front muda a URL => cache antigo nunca é reutilizado,
	// nem por caches que ignoram Cache-Control (causa de bugs "fantasma").
	sum := sha256.Sum256(append(append([]byte{}, appJS...), styleCSS...))
	assetVer = hex.EncodeToString(sum[:8])
	indexHTML = bytes.ReplaceAll(indexHTML, []byte("__VER__"), []byte(assetVer))

	mediaDir := env("MEDIA_DIR", "/data/media")
	thumbDir := env("THUMB_DIR", "/data/thumbs")

	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		log.Fatal(err)
	}

	srv := &server{
		mediaDir: mediaDir,
		thumbDir: thumbDir,
		password: env("VIEWER_PASSWORD", "Sankvault@123"),
		secret:   newSecret(filepath.Join(thumbDir, ".session_secret")),
		durs:     newDurationStore(filepath.Join(thumbDir, "meta.json")),
		docker:   newDockerMon(),
		favs:     newFavStore(filepath.Join(thumbDir, "favorites.json")),
		tags:     newTagStore(filepath.Join(thumbDir, "tags.json")),
		sem:      make(chan struct{}, 2),
		busy:     map[string]bool{},
		fails:    map[string]*failRec{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.requireAuth(srv.handleRoot))
	mux.HandleFunc("GET /static/{file}", srv.requireAuth(srv.handleStatic))
	mux.HandleFunc("GET /api/items", srv.requireAuth(srv.handleItems))
	mux.HandleFunc("GET /api/stats", srv.requireAuth(srv.handleStats))
	mux.HandleFunc("POST /api/fav/{name}", srv.requireAuth(srv.handleFavToggle))
	mux.HandleFunc("GET /api/download/status", srv.requireAuth(srv.handleDlStatus))
	mux.HandleFunc("POST /api/download/start", srv.requireAuth(srv.handleDlStart))
	mux.HandleFunc("POST /api/download/stop", srv.requireAuth(srv.handleDlStop))
	mux.HandleFunc("GET /media/{name}", srv.requireAuth(srv.handleMedia))
	mux.HandleFunc("GET /thumb/{name}", srv.requireAuth(srv.handleThumb))
	mux.HandleFunc("GET /login", srv.handleLogin)
	mux.HandleFunc("POST /login", srv.handleLogin)
	mux.HandleFunc("POST /logout", srv.handleLogout)

	addr := env("ADDR", ":8080")
	log.Printf("[sank-vault] viewer ouvindo em %s (midia=%s)", addr, mediaDir)

	srv.startDurationProber()
	log.Fatal(http.ListenAndServe(addr, noindex(mux)))
}

// ---- indice / listagem -----------------------------------------------------

// rescan devolve o indice de midias, reescaneando o volume se o cache estiver velho.
func (s *server) rescan() ([]Item, map[string]string) {
	s.mu.Lock()
	if s.items != nil && time.Since(s.indexedAt) < 8*time.Second {
		items, paths := s.items, s.paths
		s.mu.Unlock()
		return items, paths
	}
	s.mu.Unlock()

	var items []Item
	paths := map[string]string{}
	_ = filepath.WalkDir(s.mediaDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".json") {
			return nil // ocultos, downloads incompletos e sidecars de tags
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		mime := sniffMime(f)
		f.Close()
		items = append(items, Item{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
			MIME:    mime,
			Kind:    kindOf(mime),
		})
		paths[name] = path
		return nil
	})

	// enriquece com duracoes conhecidas (cache persistente de ffprobe)
	alive := make(map[string]bool, len(items))
	for i := range items {
		it := &items[i]
		if it.Kind == "video" {
			if d, ok := s.durs.lookup(it.Name, it.ModTime, it.Size); ok && d > 0 {
				it.Duration = d
			}
		}
		it.Fav = s.favs.has(it.Name)
		if e := s.tags.get(it.Name); e != nil {
			it.Tags, it.Custom, it.Title = e.Tags, e.Custom, e.Title
		} else if raw, err := os.ReadFile(filepath.Join(s.mediaDir, it.Name+".json")); err == nil {
			var side map[string]any
			if json.Unmarshal(raw, &side) == nil {
				var tg []string
				for _, k := range []string{"tags", "tag_names"} {
					if v, ok := side[k].([]any); ok {
						for _, x := range v {
							if str, ok := x.(string); ok {
								tg = append(tg, str)
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
		alive[it.Name] = true
	}
	s.durs.prune(alive)
	s.tags.prune(alive)
	s.tags.save()

	sort.Slice(items, func(i, j int) bool { return items[i].ModTime > items[j].ModTime })

	s.mu.Lock()
	s.items, s.paths, s.indexedAt = items, paths, time.Now()
	s.mu.Unlock()
	return items, paths
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// catOf deriva a aba da UI a partir do mime: videos, gifs (animados ou nao),
// imagens que o navegador renderiza; o resto so aparece em "all".
func catOf(it Item) string {
	switch {
	case it.Kind == "video":
		return "video"
	case it.MIME == "image/gif":
		return "gif"
	case it.Kind == "image":
		return "image"
	default:
		return "other" // heic, tiff, desconhecidos...
	}
}

var validCats = map[string]bool{"all": true, "image": true, "gif": true, "video": true, "fav": true}

func applySort(items []Item, key string) {
	switch key {
	case "oldest":
		sort.SliceStable(items, func(i, j int) bool { return items[i].ModTime < items[j].ModTime })
	case "size_asc":
		sort.SliceStable(items, func(i, j int) bool { return items[i].Size < items[j].Size })
	case "size_desc":
		sort.SliceStable(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	case "duration_asc", "duration_desc":
		asc := key == "duration_asc"
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].Duration > 0, items[j].Duration > 0
			if a != b { // sem duração conhecida vai sempre pro fim
				return b
			}
			if asc {
				return a && items[i].Duration < items[j].Duration
			}
			return items[i].Duration > items[j].Duration
		})
	case "name":
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
	default: // "recent": mtime desc — já é a ordem do índice
	}
}

// applyRandom embaralha de forma DETERMINÍSTICA: a posição de cada mídia
// depende apenas de (seed, nome). Com o mesmo seed, todas as páginas voltam
// na mesma ordem => paginação/scroll infinito estável. Seed 0 = aleatório
// de verdade (o servidor gera um e devolve pro cliente fixar).
func applyRandom(items []Item, seed uint64) uint64 {
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	type kv struct {
		it    Item
		score uint64
	}
	buf := make([]kv, len(items))
	for i, it := range items {
		h := fnv.New64a()
		var sb [8]byte
		binary.BigEndian.PutUint64(sb[:], seed)
		h.Write(sb[:])
		io.WriteString(h, it.Name)
		buf[i] = kv{it, h.Sum64()}
	}
	sort.SliceStable(buf, func(i, j int) bool { return buf[i].score < buf[j].score })
	for i := range buf {
		items[i] = buf[i].it
	}
	return seed
}

func (s *server) handleItems(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.FormValue("q")))
	cat := r.FormValue("category")
	if !validCats[cat] {
		cat = "all"
	}
	sortKey := r.FormValue("sort")
	seed, _ := strconv.ParseUint(r.FormValue("seed"), 10, 64)
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	limit, _ := strconv.Atoi(r.FormValue("limit"))
	if limit <= 0 || limit > 200 {
		limit = 60
	}

	items, _ := s.rescan()
	filtered := make([]Item, 0, len(items))
	for _, it := range items {
		if cat == "fav" && !it.Fav {
			continue
		}
		if cat != "all" && cat != "fav" && catOf(it) != cat {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) {
			continue
		}
		filtered = append(filtered, it)
	}
	if sortKey == "random" {
		seed = applyRandom(filtered, seed)
	} else {
		applySort(filtered, sortKey)
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := filtered[offset:end]
	if page == nil {
		page = []Item{}
	}
	resp := map[string]any{"total": total, "items": page}
	if sortKey == "random" {
		resp["seed"] = seed // cliente fixa o seed usado nas próximas páginas
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	items, _ := s.rescan()
	counts := map[string]int{"all": len(items), "image": 0, "gif": 0, "video": 0, "fav": 0}
	for _, it := range items {
		if c := catOf(it); c != "other" {
			counts[c]++
		}
		if it.Fav {
			counts["fav"]++
		}
	}
	left, total := s.probeStats()
	st := map[string]any{
		"counts": counts,
		"probe":  map[string]int{"left": left, "total": total},
	}
	var totalBytes int64
	for _, it := range items {
		totalBytes += it.Size
	}
	st["total_bytes"] = totalBytes
	// versão do índice para o front detectar mudanças entre polls
	s.mu.Lock()
	st["indexedAt"] = s.indexedAt.Unix()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

// ---- favoritos --------------------------------------------------------------

// handleFavToggle alterna a estrela de uma mídia (POST /api/fav/{name}).
func (s *server) handleFavToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, paths := s.rescan(); func() bool { _, ok := paths[name]; return !ok }() {
		http.NotFound(w, r)
		return
	}
	fav := s.favs.toggle(name)
	// reflete já no índice em cache (rescan tem TTL de 8s)
	s.mu.Lock()
	for i := range s.items {
		if s.items[i].Name == name {
			s.items[i].Fav = fav
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"fav": fav})
}

// ---- servico de midia ------------------------------------------------------

func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	_, paths := s.rescan()
	name := r.PathValue("name")
	path, ok := paths[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "arquivo indisponivel", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	mime := sniffMime(f)

	w.Header().Set("Content-Type", mime)
	// id_md5 e unico e imutavel: o browser pode guardar para sempre
	// (reabrir um video do lightbox nao re-transfere nada)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(name))
	// ServeContent cuida de Range/206/If-Range -> seeking funciona nos videos.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// nunca cachear: mudanças no front precisam valer em reload simples,
	// inclusive no celular (cache do Chromium foi a origem de bugs "fantasma")
	w.Header().Set("Cache-Control", "no-store")
	switch r.PathValue("file") {
	case "app.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(appJS)
	case "style.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(styleCSS)
	default:
		http.NotFound(w, r)
	}
}

// ---- auth wrapper ----------------------------------------------------------

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validSession(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "nao autenticado"})
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}
