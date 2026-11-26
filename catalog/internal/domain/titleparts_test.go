package domain

import "testing"

func TestSplitTitle(t *testing.T) {
	cases := []struct {
		title, name     string
		season, disc    int
		seasonIn, disc0 bool // disc0: no disc in the title
	}{
		{"Friends S01 D2", "Friends", 1, 2, true, false},
		{"Lost - Season 2 Disc 3", "Lost", 2, 3, true, false},
		{"Dexter T1 Disco 4", "Dexter", 1, 4, true, false},
		{"Scoobydoo Where Are You S1 D1", "Scoobydoo Where Are You", 1, 1, true, false},
		{"Smallville S2", "Smallville", 2, 0, true, true},
		{"The Wire S03D02", "The Wire", 3, 2, true, false},
		{"Titanic Disc 2", "Titanic", 0, 2, false, false},
		{"Gandhi (Disco 1 de 2)", "Gandhi", 0, 1, false, false},
	}
	for _, c := range cases {
		p := SplitTitle(c.title)
		if !p.Named || p.Name != c.name || p.Season != c.season || p.SeasonIn != c.seasonIn ||
			p.Disc != c.disc || p.DiscIn == c.disc0 {
			t.Errorf("SplitTitle(%q) = %+v; want %q season %d (%v) disc %d (%v)", c.title, p, c.name, c.season, c.seasonIn, c.disc, !c.disc0)
		}
	}
	// Titles that name no season or disc stay whole.
	for _, title := range []string{"T2 Trainspotting", "Apollo 13", "Se7en", "The Lighthouse"} {
		if p := SplitTitle(title); p.Named || p.Name != title {
			t.Errorf("SplitTitle(%q) = %+v; want it whole", title, p)
		}
	}
}

func TestAutoParts(t *testing.T) {
	// The title says it all.
	d := &Disc{Kind: "dvd", Rel: "SCOOBYDOO_WHERE_ARE_YOU_S1_D1.iso"}
	if name, season, disc := AutoParts(d, "Scoobydoo Where Are You S1 D1"); name != "Scoobydoo Where Are You" || season != int64(1) || disc != int64(1) {
		t.Errorf("from the title: %q %v %v", name, season, disc)
	}
	// A title found online names no season: the numbers come from the file name, and the title stays.
	if name, season, disc := AutoParts(d, "Scooby-Doo, Where Are You!"); name != "Scooby-Doo, Where Are You!" || season != int64(1) || disc != int64(1) {
		t.Errorf("from the file name: %q %v %v", name, season, disc)
	}
	// Or from the volume label.
	l := &Disc{Kind: "dvd", Rel: "disc.iso", Label: "FRIENDS_S02_D3"}
	if _, season, disc := AutoParts(l, "Friends"); season != int64(2) || disc != int64(3) {
		t.Errorf("from the label: %v %v", season, disc)
	}
	// A film alone: nothing to fill in.
	f := &Disc{Kind: "dvd", Rel: "Now and Then.iso"}
	if name, season, disc := AutoParts(f, "Now and Then"); name != "Now and Then" || season != nil || disc != nil {
		t.Errorf("a film: %q %v %v", name, season, disc)
	}
	// A CD is left alone.
	c := &Disc{Kind: "cd", Rel: "Album Disc 2.cue"}
	if name, season, disc := AutoParts(c, "Album Disc 2"); name != "Album Disc 2" || season != nil || disc != nil {
		t.Errorf("a CD: %q %v %v", name, season, disc)
	}
}
