package main

import (
	"embed"
	"encoding/json"
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
)

// Item descreve uma midia do vault para a API/UI.
type Item struct {
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	ModTime  int64   `json:"mtime"`
	MIME     string  `json:"mime"`
	Kind     string  `json:"kind"`               // image | video | other
	Duration float64 `json:"duration,omitempty"` // segundos (apenas videos)
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

	durs *durationStore

	probeMu    sync.Mutex
	probeLeft  int // videos na fila de analise de duracao
	probeTotal int

	tmu  sync.Mutex
	busy map[string]bool // geracao de thumb em andamento
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
		password: env("VIEWER_PASSWORD", "sankvault"),
		secret:   newSecret(),
		durs:     newDurationStore(filepath.Join(thumbDir, "meta.json")),
		busy:     map[string]bool{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.requireAuth(srv.handleRoot))
	mux.HandleFunc("GET /static/{file}", srv.requireAuth(srv.handleStatic))
	mux.HandleFunc("GET /api/items", srv.requireAuth(srv.handleItems))
	mux.HandleFunc("GET /api/stats", srv.requireAuth(srv.handleStats))
	mux.HandleFunc("GET /media/{name}", srv.requireAuth(srv.handleMedia))
	mux.HandleFunc("GET /thumb/{name}", srv.requireAuth(srv.handleThumb))
	mux.HandleFunc("GET /login", srv.handleLogin)
	mux.HandleFunc("POST /login", srv.handleLogin)
	mux.HandleFunc("POST /logout", srv.handleLogout)

	addr := env("ADDR", ":8080")
	log.Printf("[sank-vault] viewer ouvindo em %s (midia=%s)", addr, mediaDir)

	srv.startDurationProber()
	log.Fatal(http.ListenAndServe(addr, mux))
}

// ---- indice / listagem -----------------------------------------------------

// rescan devolve o indice de midias, reescaneando o volume se o cache estiver velho.
func (s *server) rescan() ([]Item, map[string]string) {
	s.mu.Lock()
	if s.items != nil && time.Since(s.indexedAt) < 3*time.Second {
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
		if strings.HasPrefix(name, ".") {
			return nil
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
		alive[it.Name] = true
	}
	s.durs.prune(alive)

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

var validCats = map[string]bool{"all": true, "image": true, "gif": true, "video": true}

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

func (s *server) handleItems(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.FormValue("q")))
	cat := r.FormValue("category")
	if !validCats[cat] {
		cat = "all"
	}
	sortKey := r.FormValue("sort")
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	limit, _ := strconv.Atoi(r.FormValue("limit"))
	if limit <= 0 || limit > 200 {
		limit = 60
	}

	items, _ := s.rescan()
	filtered := make([]Item, 0, len(items))
	for _, it := range items {
		if cat != "all" && catOf(it) != cat {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) {
			continue
		}
		filtered = append(filtered, it)
	}
	applySort(filtered, sortKey)

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
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "items": page})
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	items, _ := s.rescan()
	counts := map[string]int{"all": len(items), "image": 0, "gif": 0, "video": 0}
	for _, it := range items {
		if c := catOf(it); c != "other" {
			counts[c]++
		}
	}
	left, total := s.probeStats()
	st := map[string]any{
		"counts": counts,
		"probe":  map[string]int{"left": left, "total": total},
	}
	// versão do índice para o front detectar mudanças entre polls
	s.mu.Lock()
	st["indexedAt"] = s.indexedAt.Unix()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, st)
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
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(name))
	// ServeContent cuida de Range/206/If-Range -> seeking funciona nos videos.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
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
