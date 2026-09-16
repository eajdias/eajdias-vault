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

func TestSortDurationUnknownLast(t *testing.T) {
	for _, key := range []string{"duration_asc", "duration_desc"} {
		items := []Item{{Name: "nodur", Duration: 0}, {Name: "a", Duration: 10}, {Name: "b", Duration: 5}}
		applySort(items, key)
		if items[2].Name != "nodur" {
			t.Errorf("%s: sem-duracao deveria ficar no fim, ordem=%s %s %s", key, items[0].Name, items[1].Name, items[2].Name)
		}
		if key == "duration_asc" && (items[0].Name != "b" || items[1].Name != "a") {
			t.Errorf("asc fora de ordem: %s %s", items[0].Name, items[1].Name)
		}
		if key == "duration_desc" && (items[0].Name != "a" || items[1].Name != "b") {
			t.Errorf("desc fora de ordem: %s %s", items[0].Name, items[1].Name)
		}
	}
}
