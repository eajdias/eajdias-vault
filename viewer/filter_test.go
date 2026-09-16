package main

import "testing"

func TestMatchTagsAND(t *testing.T) {
	it := Item{Name: "1_a", Tags: []string{"1girl", "solo"}, Custom: []string{"minha_tag"}}
	if !matchTags(it, []string{}) {
		t.Error("filtro vazio deve casar tudo")
	}
	if !matchTags(it, []string{"1girl", "solo"}) {
		t.Error("AND com 2 tags da fonte deve casar")
	}
	if !matchTags(it, []string{"1GIRL", "Minha Tag"}) {
		t.Error("match deve normalizar")
	}
	if !matchTags(it, []string{"solo", "minha_tag"}) {
		t.Error("fonte+custom devem unir")
	}
	if matchTags(it, []string{"1girl", "inexistente"}) {
		t.Error("AND com tag ausente nao deve casar")
	}
}
