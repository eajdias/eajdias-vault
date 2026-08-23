package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
)

// favStore guarda os favoritos do usuário em um JSON simples dentro do
// volume de thumbs (/data/thumbs/favorites.json). O nome da mídia é único
// e imutável, então serve de chave estável.
type favStore struct {
	mu   sync.Mutex
	path string
	set  map[string]bool
}

func newFavStore(path string) *favStore {
	f := &favStore{path: path, set: map[string]bool{}}
	if data, err := os.ReadFile(path); err == nil {
		var names []string
		if json.Unmarshal(data, &names) == nil {
			for _, n := range names {
				f.set[n] = true
			}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[fav] não consegui ler %s: %v", path, err)
	}
	return f
}

func (f *favStore) has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.set[name]
}

// toggle alterna e persiste; devolve o novo estado.
func (f *favStore) toggle(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.set[name] {
		delete(f.set, name)
	} else {
		f.set[name] = true
	}
	names := make([]string, 0, len(f.set))
	for n := range f.set {
		names = append(names, n)
	}
	data, err := json.Marshal(names)
	if err == nil {
		tmp := f.path + ".tmp"
		if os.WriteFile(tmp, data, 0o644) == nil {
			os.Rename(tmp, f.path)
		}
	} else {
		log.Printf("[fav] marshal: %v", err)
	}
	return f.set[name]
}
