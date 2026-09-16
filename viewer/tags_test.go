package main

import (
	"path/filepath"
	"testing"
)

func TestNormTag(t *testing.T) {
	cases := map[string]string{
		"Blue Archive":    "blue_archive",
		"  LONG_hair ":    "long_hair",
		"1GIRL":           "1girl",
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
