package domain

import (
	"reflect"
	"testing"
)

func names(tracks []map[string]any) []string {
	out := []string{}
	for _, t := range tracks {
		out = append(out, t["name"].(string))
	}
	return out
}

func TestRelabelTracks(t *testing.T) {
	// What VLC lists for a Blu-ray with menus: every track named after the disc's first clip.
	subs := []map[string]any{
		{"id": -1, "name": "Disable", "selected": true},
		{"id": 4608, "name": "Track 1 - [English]", "selected": false},
		{"id": 4609, "name": "Track 2 - [English]", "selected": false},
		{"id": 4610, "name": "Track 3", "selected": false},
	}
	got := RelabelTracks(subs, []string{"eng", "fra", "por"})
	want := []string{"Disable", "Track 1 - [English]", "Track 2 - [French]", "Track 3 - [Portuguese]"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("names = %v, want %v", names(got), want)
	}
	if got[2]["id"] != 4609 || got[0]["selected"] != true {
		t.Errorf("ids and selection must stay: %v", got)
	}
	if subs[2]["name"] != "Track 2 - [English]" {
		t.Errorf("the input must not change: %v", subs[2])
	}
	// A list that does not match the tracks is not applied.
	if got := RelabelTracks(subs, []string{"eng", "fra"}); !reflect.DeepEqual(names(got), names(subs)) {
		t.Errorf("mismatched list applied: %v", names(got))
	}
}

func TestLanguageName(t *testing.T) {
	for code, want := range map[string]string{"eng": "English", "fre": "French", "fra": "French", "POR": "Portuguese", "xyz": "XYZ"} {
		if got := LanguageName(code); got != want {
			t.Errorf("LanguageName(%q) = %q, want %q", code, got, want)
		}
	}
}
