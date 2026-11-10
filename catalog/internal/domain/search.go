package domain

import (
	"regexp"
	"streamiso/internal/compat"
)

// SearchTerms is what to search TMDB for. A manual query may name the year and season too:
// "Will & Grace (2017) S01 D2". A film on several discs is searched without its "Disc 2".
// DiscSeason and AutoTitle are what an automatic lookup searches for, so enrichAll can tell when
// to look the disc up again. rec holds the user's edits of the disc.
type SearchTerms struct {
	Title      string
	Year       any
	Season     any
	TVYear     any
	DiscSeason any
	AutoTitle  string
}

func NewSearchTerms(d *Disc, query string, rec map[string]any) SearchTerms {
	series, season := DiscSeason(d, rec)
	discSeasonNo := season
	var autoTitle string
	if season != nil {
		autoTitle = series
	} else {
		t := d.Title
		if compat.Truthy(rec["title"]) {
			t = compat.Str(rec["title"])
		}
		autoTitle = FilmName(t)
	}
	var year any
	if d.Year != "" {
		year = d.Year
	}
	if query != "" { // "Will & Grace (2017)": the year picks the film or series of that year
		if m := yearInParensRE.FindStringSubmatch(query); m != nil {
			year = m[1]
		} else {
			year = nil
		}
		if q := compat.Strip(compat.CollapseSpace(yearParensRE.ReplaceAllString(query, " "))); q != "" {
			query = q
		}
		if name, s, ok := ParseSeason(query); ok {
			query, season = name, s
		} else {
			query = FilmName(query)
		}
	}
	t := SearchTerms{Title: query, Year: year, Season: season, DiscSeason: discSeasonNo, AutoTitle: autoTitle}
	if query == "" {
		t.Title = autoTitle
		t.TVYear = compat.FirstTruthy(rec["year"], year)
		if !compat.Truthy(year) && rec["year"] == nil {
			t.TVYear = nil
		}
	} else {
		t.TVYear = year
	}
	return t
}

var (
	yearInParensRE = regexp.MustCompile(`\(((?:19|20)\d{2})\)`)
	yearParensRE   = regexp.MustCompile(`\((?:19|20)\d{2}\)`)
)
