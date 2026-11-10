package main

// Checks the Go version against what the Python catalog answers for the same inputs.
// testdata/golden.json is written by tools/golden.py from ../catalog (see README.md).

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
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
		path = "testdata/golden.json"
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

func variantsAny(vs []variant) [][]any {
	out := [][]any{}
	for _, v := range vs {
		out = append(out, []any{v.query, v.strict})
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
		title, year := cleanTitle(c.In)
		wantYear := ""
		if c.CleanTitle[1] != nil {
			wantYear = *c.CleanTitle[1]
		}
		if title != *c.CleanTitle[0] || year != wantYear {
			t.Errorf("cleanTitle(%q) = %q, %q; want %q, %q", c.In, title, year, *c.CleanTitle[0], wantYear)
		}
		if got := isGeneric(c.In); got != c.IsGeneric {
			t.Errorf("isGeneric(%q) = %v", c.In, got)
		}
		if got := pair(parseSeason(c.In)); asJSON(got) != asJSON(c.ParseSeason) {
			t.Errorf("parseSeason(%q) = %v; want %v", c.In, got, c.ParseSeason)
		}
		if got := pair(parseFilmDisc(c.In)); asJSON(got) != asJSON(c.ParseFilmDisc) {
			t.Errorf("parseFilmDisc(%q) = %v; want %v", c.In, got, c.ParseFilmDisc)
		}
		if got := filmName(c.In); got != c.FilmName {
			t.Errorf("filmName(%q) = %q; want %q", c.In, got, c.FilmName)
		}
		if got := orEmpty(words(c.In)); !reflect.DeepEqual(got, orEmpty(c.Words)) {
			t.Errorf("words(%q) = %q; want %q", c.In, got, c.Words)
		}
		if got := variantsAny(titleVariants(c.In)); asJSON(got) != asJSON(orEmpty(c.TitleVariants)) {
			t.Errorf("titleVariants(%q) = %v; want %v", c.In, got, c.TitleVariants)
		}
		if got := orEmpty(textwrapWrap(c.In, 14)); !reflect.DeepEqual(got, orEmpty(c.Wrap)) {
			t.Errorf("wrap(%q) = %q; want %q", c.In, got, c.Wrap)
		}
		if got := pyTitle(c.In); got != c.Title {
			t.Errorf("title(%q) = %q; want %q", c.In, got, c.Title)
		}
		var year2 any
		if len([]rune(c.In))%2 == 1 {
			year2 = 1999
		}
		if got := placeholderSVG(c.In, year2, len([]rune(c.In))%3 == 0); got != c.SVG {
			t.Errorf("placeholderSVG(%q):\n got %s\nwant %s", c.In, got, c.SVG)
		}
		if got := variantsAny(mbVariants(c.In)); asJSON(got) != asJSON(orEmpty(c.MBVariants)) {
			t.Errorf("mbVariants(%q) = %v; want %v", c.In, got, c.MBVariants)
		}
		if got := mbNorm(c.In); got != c.MBNorm {
			t.Errorf("mbNorm(%q) = %q; want %q", c.In, got, c.MBNorm)
		}
		if got := casefold(c.In); got != c.Casefold {
			t.Errorf("casefold(%q) = %q; want %q", c.In, got, c.Casefold)
		}
	}
}

func TestGoldenRest(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.IsPartOf {
		if got := isPartOf(c[0].(string), c[1].(string)); got != c[2].(bool) {
			t.Errorf("isPartOf(%q, %q) = %v", c[0], c[1], got)
		}
	}
	for _, c := range g.Localized {
		meta := map[string]any{}
		for k, v := range c[2].(map[string]any) {
			meta[k] = v
		}
		d := &Disc{Title: c[0].(string), Kind: c[1].(string)}
		if got := localizedTitle(d, meta); got != c[3].(string) {
			t.Errorf("localizedTitle(%q, %s, %v) = %q; want %q", c[0], c[1], c[2], got, c[3])
		}
	}
	for _, c := range g.CleanMeta {
		// the payload goes through the decoder the server uses (numbers as json.Number)
		raw, _ := json.Marshal(c[0])
		v, _ := decodeJSON(raw)
		got, err := cleanMeta(v.(map[string]any))
		if c[2] != nil {
			if err == nil || err.Error() != c[2].(string) {
				t.Errorf("cleanMeta(%s) error = %v; want %q", raw, err, c[2])
			}
			continue
		}
		if err != nil {
			t.Errorf("cleanMeta(%s) error %v", raw, err)
			continue
		}
		if asJSON(got) != asJSON(c[1]) {
			t.Errorf("cleanMeta(%s) = %s; want %s", raw, asJSON(got), asJSON(c[1]))
		}
	}
	for _, c := range g.Round {
		if got := pyRound(c[0]); float64(got) != c[1] {
			t.Errorf("round(%v) = %v; want %v", c[0], got, c[1])
		}
		if got := pyRound2(c[0]); got != c[2] {
			t.Errorf("round(%v, 2) = %v; want %v", c[0], got, c[2])
		}
	}
}
