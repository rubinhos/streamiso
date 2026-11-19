package domain

import (
	"reflect"
	"testing"
)

func TestDVDMenus(t *testing.T) {
	l := &DVDLayout{
		TitleMenu: true,
		TitleSets: []int{1, 2, 3},
		SetMenus: map[int]map[string]bool{
			1: {MenuRoot: true, MenuChapters: true, MenuAudio: true},
			2: {MenuAudio: true}, // no Root menu: VLC would jump inside the title instead
			3: {MenuRoot: true, MenuSubtitles: true},
		},
	}
	cases := []struct {
		name  string
		title int64
		set   int
		want  []string
	}{
		{"film", 1, 0, []string{MenuTitle, MenuRoot, MenuChapters, MenuAudio}},
		{"title set without Root", 2, 0, []string{MenuTitle}},
		{"menu after the film", 0, 1, []string{MenuTitle, MenuRoot, MenuChapters, MenuAudio}},
		{"menu, set unknown", 0, 0, []string{MenuTitle, MenuRoot, MenuChapters, MenuAudio, MenuSubtitles}},
		{"title past the list", 9, 0, []string{MenuTitle}},
	}
	for _, c := range cases {
		if got := l.Menus(c.title, c.set); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	var none *DVDLayout
	if got := none.Menus(1, 0); len(got) != 0 {
		t.Errorf("no layout: got %v", got)
	}
}
