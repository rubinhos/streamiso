// Package golden checks the catalog's rules against testdata/golden.json: the answers its first version
// (in Python) gave for the same inputs, which the API and the stored files still follow.
package golden

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"streamiso/internal/adapters/musicbrainz"
	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"testing"
)

type golden struct {
	Titles []struct {
		In            string     `json:"in"`
		CleanTitle    [2]*string `json:"clean_title"`
		IsGeneric     bool       `json:"is_generic"`
		ParseSeason   []any      `json:"parse_season"`
		ParseFilmDisc []any      `json:"parse_film_disc"`
		FilmName      string     `json:"film_name"`
		Words         []string   `json:"words"`
		TitleVariants [][]any    `json:"title_variants"`
		Wrap          []string   `json:"wrap"`
		Title         string     `json:"title"`
		SVG           string     `json:"svg"`
		MBVariants    [][]any    `json:"mb_variants"`
		MBNorm        string     `json:"mb_norm"`
		Casefold      string     `json:"casefold"`
	} `json:"titles"`
	IsPartOf  [][]any     `json:"is_part_of"`
	Localized [][]any     `json:"localized"`
	CleanMeta [][]any     `json:"clean_meta"`
	Round     [][]float64 `json:"round"`
}

func loadGolden(t *testing.T) golden {
	path := os.Getenv("GOLDEN")
	if path == "" {
		path = "../../testdata/golden.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no golden file: %v", err)
	}
	var g golden
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keeps 5.0 apart from 5, as Python does
	if err := dec.Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

func pair(name string, n int, ok bool) []any {
	if !ok {
		return nil
	}
	return []any{name, float64(n)}
}

func variantsAny(vs []domain.Variant) [][]any {
	out := [][]any{}
	for _, v := range vs {
		out = append(out, []any{v.Query, v.Strict})
	}
	return out
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// asJSON compares values the way JSON sees them (ints and floats alike, nil and empty lists apart).
func asJSON(v any) string {
	b, _ := json.Marshal(v)
	var x any
	_ = json.Unmarshal(b, &x)
	b, _ = json.Marshal(x)
	return string(b)
}

func TestGoldenTitles(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Titles {
		title, year := domain.CleanTitle(c.In)
		wantYear := ""
		if c.CleanTitle[1] != nil {
			wantYear = *c.CleanTitle[1]
		}
		if title != *c.CleanTitle[0] || year != wantYear {
			t.Errorf("domain.CleanTitle(%q) = %q, %q; want %q, %q", c.In, title, year, *c.CleanTitle[0], wantYear)
		}
		if got := domain.IsGeneric(c.In); got != c.IsGeneric {
			t.Errorf("domain.IsGeneric(%q) = %v", c.In, got)
		}
		if got := pair(domain.ParseSeason(c.In)); asJSON(got) != asJSON(c.ParseSeason) {
			t.Errorf("domain.ParseSeason(%q) = %v; want %v", c.In, got, c.ParseSeason)
		}
		if got := pair(domain.ParseFilmDisc(c.In)); asJSON(got) != asJSON(c.ParseFilmDisc) {
			t.Errorf("domain.ParseFilmDisc(%q) = %v; want %v", c.In, got, c.ParseFilmDisc)
		}
		if got := domain.FilmName(c.In); got != c.FilmName {
			t.Errorf("domain.FilmName(%q) = %q; want %q", c.In, got, c.FilmName)
		}
		if got := orEmpty(domain.Words(c.In)); !reflect.DeepEqual(got, orEmpty(c.Words)) {
			t.Errorf("domain.Words(%q) = %q; want %q", c.In, got, c.Words)
		}
		if got := variantsAny(domain.TitleVariants(c.In)); asJSON(got) != asJSON(orEmpty(c.TitleVariants)) {
			t.Errorf("domain.TitleVariants(%q) = %v; want %v", c.In, got, c.TitleVariants)
		}
		if got := orEmpty(compat.Wrap(c.In, 14)); !reflect.DeepEqual(got, orEmpty(c.Wrap)) {
			t.Errorf("wrap(%q) = %q; want %q", c.In, got, c.Wrap)
		}
		if got := compat.Title(c.In); got != c.Title {
			t.Errorf("title(%q) = %q; want %q", c.In, got, c.Title)
		}
		var year2 any
		if len([]rune(c.In))%2 == 1 {
			year2 = 1999
		}
		if got := domain.PlaceholderSVG(c.In, year2, len([]rune(c.In))%3 == 0); got != c.SVG {
			t.Errorf("domain.PlaceholderSVG(%q):\n got %s\nwant %s", c.In, got, c.SVG)
		}
		if got := variantsAny(musicbrainz.Variants(c.In)); asJSON(got) != asJSON(orEmpty(c.MBVariants)) {
			t.Errorf("musicbrainz.Variants(%q) = %v; want %v", c.In, got, c.MBVariants)
		}
		if got := musicbrainz.Norm(c.In); got != c.MBNorm {
			t.Errorf("musicbrainz.Norm(%q) = %q; want %q", c.In, got, c.MBNorm)
		}
		if got := compat.Casefold(c.In); got != c.Casefold {
			t.Errorf("compat.Casefold(%q) = %q; want %q", c.In, got, c.Casefold)
		}
	}
}

func TestGoldenRest(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.IsPartOf {
		if got := domain.IsPartOf(c[0].(string), c[1].(string)); got != c[2].(bool) {
			t.Errorf("domain.IsPartOf(%q, %q) = %v", c[0], c[1], got)
		}
	}
	for _, c := range g.Localized {
		meta := map[string]any{}
		for k, v := range c[2].(map[string]any) {
			meta[k] = v
		}
		d := &domain.Disc{Title: c[0].(string), Kind: c[1].(string)}
		if got := domain.LocalizedTitle(d, meta); got != c[3].(string) {
			t.Errorf("domain.LocalizedTitle(%q, %s, %v) = %q; want %q", c[0], c[1], c[2], got, c[3])
		}
	}
	for _, c := range g.CleanMeta {
		// the payload goes through the decoder the server uses (numbers as json.Number)
		raw, _ := json.Marshal(c[0])
		v, _ := compat.DecodeJSON(raw)
		got, err := domain.CleanMeta(v.(map[string]any))
		if c[2] != nil {
			if err == nil || err.Error() != c[2].(string) {
				t.Errorf("domain.CleanMeta(%s) error = %v; want %q", raw, err, c[2])
			}
			continue
		}
		if err != nil {
			t.Errorf("domain.CleanMeta(%s) error %v", raw, err)
			continue
		}
		if asJSON(got) != asJSON(c[1]) {
			t.Errorf("domain.CleanMeta(%s) = %s; want %s", raw, asJSON(got), asJSON(c[1]))
		}
	}
	for _, c := range g.Round {
		if got := compat.Round(c[0]); float64(got) != c[1] {
			t.Errorf("round(%v) = %v; want %v", c[0], got, c[1])
		}
		if got := compat.Round2(c[0]); got != c[2] {
			t.Errorf("round(%v, 2) = %v; want %v", c[0], got, c[2])
		}
	}
}
