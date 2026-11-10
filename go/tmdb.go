package main

// Metadata and covers: TMDB for DVDs (needs a key), MusicBrainz for CDs.

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const fallbackLanguage = "en-US"

var (
	genreMu    sync.Mutex
	genreCache = map[string]map[string]string{}
)

// tmdbGet calls the TMDB API. params are added in order, then the key.
func tmdbGet(path string, params ...[2]string) (map[string]any, error) {
	q := make([]string, 0, len(params)+1)
	for _, p := range params {
		q = append(q, url.QueryEscape(p[0])+"="+url.QueryEscape(p[1]))
	}
	q = append(q, "api_key="+url.QueryEscape(cfg.TMDBKey))
	body, err := httpGet(cfg.TMDBAPI+path+"?"+strings.Join(q, "&"),
		map[string]string{"User-Agent": "streamiso/" + version}, 15*time.Second, 0)
	if err != nil {
		return nil, err
	}
	v, err := decodeJSON(body)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("TMDB sent no object")
	}
	return m, nil
}

func lang(l string) [2]string { return [2]string{"language", l} }

// numKey is the text of a JSON number as a map key (TMDB ids).
func numKey(v any) string {
	if f, ok := toFloat(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return pyStr(v)
}

func tmdbGenreMap(kind string) (map[string]string, error) {
	genreMu.Lock()
	defer genreMu.Unlock()
	if m, ok := genreCache[kind]; ok {
		return m, nil
	}
	data, err := tmdbGet("/genre/"+kind+"/list", lang(cfg.TMDBLanguage))
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	list, _ := data["genres"].([]any)
	for _, g := range list {
		gm, _ := g.(map[string]any)
		id, okID := gm["id"]
		name, okName := gm["name"]
		if !okID || !okName {
			return nil, fmt.Errorf("KeyError")
		}
		m[numKey(id)] = pyStr(name)
	}
	genreCache[kind] = m
	return m, nil
}

func results(data map[string]any) []map[string]any {
	list, _ := data["results"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, r := range list {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// firstTruthy is (a or b or ... or def).
func firstTruthy(def any, vals ...any) any {
	for _, v := range vals {
		if truthy(v) {
			return v
		}
	}
	return def
}

func resultNames(r map[string]any) string {
	var parts []string
	for _, k := range []string{"title", "name", "original_title", "original_name"} {
		if truthy(r[k]) {
			parts = append(parts, pyStr(r[k]))
		}
	}
	return strings.Join(parts, " ")
}

func resultYear(r map[string]any) string {
	return runeSlice(pyStr(firstTruthy("", r["release_date"], r["first_air_date"])), 4)
}

func getOr(m map[string]any, key string, def any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

// tmdbLookup finds the disc's film or series. year filters films; tvYear only puts first a
// series that started that year (a revival next to the original), since a season disc's year is
// often the season's, not the series'.
func tmdbLookup(title string, year any, preferTV bool, tvYear any) (map[string]any, error) {
	kinds := []string{"movie", "tv"}
	if preferTV {
		kinds = []string{"tv", "movie"}
	}
	for _, v := range titleVariants(title) {
		want := wordSet(v.query)
		for _, kind := range kinds {
			params := [][2]string{{"query", v.query}, lang(cfg.TMDBLanguage), {"include_adult", "false"}}
			if truthy(year) && kind == "movie" {
				params = append(params, [2]string{"year", pyStr(year)})
			}
			data, err := tmdbGet("/search/"+kind, params...)
			if err != nil {
				return nil, err
			}
			res := results(data)
			if truthy(tvYear) && kind == "tv" {
				ty := pyStr(tvYear)
				sort.SliceStable(res, func(i, j int) bool {
					a := runeSlice(pyStr(firstTruthy("", res[i]["first_air_date"])), 4) != ty
					b := runeSlice(pyStr(firstTruthy("", res[j]["first_air_date"])), 4) != ty
					return !a && b
				})
			}
			for _, r := range res {
				if !truthy(r["poster_path"]) {
					continue
				}
				if v.strict && !subset(want, wordSet(resultNames(r))) {
					continue
				}
				gmap, err := tmdbGenreMap(kind)
				if err != nil {
					return nil, err
				}
				id, ok := r["id"]
				if !ok {
					return nil, fmt.Errorf("KeyError: 'id'")
				}
				genres := []any{}
				ids, _ := getOr(r, "genre_ids", []any{}).([]any)
				for _, gid := range ids {
					if name, ok := gmap[numKey(gid)]; ok {
						genres = append(genres, name)
					}
				}
				return map[string]any{
					"poster_path": r["poster_path"],
					"tmdb_kind":   kind,
					"tmdb_id":     id,
					"overview":    getOr(r, "overview", ""),
					"tmdb_title":  firstTruthy("", r["title"], r["name"]),
					"tmdb_year":   resultYear(r),
					"rating":      r["vote_average"],
					"genres":      genres,
					"matched":     v.query,
				}, nil
			}
		}
	}
	return nil, nil
}

// tmdbOverview is the synopsis in TMDB_LANGUAGE, or else in English: TMDB sends an empty one for
// a language the title has no translation in.
func tmdbOverview(kind string, id any, overview any) (any, error) {
	if truthy(overview) || cfg.TMDBLanguage == fallbackLanguage {
		return overview, nil
	}
	data, err := tmdbGet("/"+kind+"/"+pyStr(id), lang(fallbackLanguage))
	if err != nil {
		return nil, err
	}
	return firstTruthy("", data["overview"]), nil
}

// searchTerms is what to search TMDB for. A manual query may name the year and season too:
// "Will & Grace (2017) S01 D2". A film on several discs is searched without its "Disc 2".
// DiscSeason and AutoTitle are what an automatic lookup searches for, so enrichAll can tell when
// to look the disc up again. rec holds the user's edits (read from library.json when nil).
type searchTermsT struct {
	Title      string
	Year       any
	Season     any
	TVYear     any
	DiscSeason any
	AutoTitle  string
}

var (
	yearInParensRE = regexp.MustCompile(`\(((?:19|20)\d{2})\)`)
	yearParensRE   = regexp.MustCompile(`\((?:19|20)\d{2}\)`)
)

func searchTerms(d *Disc, query string, rec map[string]any) searchTermsT {
	if rec == nil {
		rec = libRec(loadLibrary(), d.FP)
	}
	series, season := discSeason(d, rec)
	discSeasonNo := season
	var autoTitle string
	if season != nil {
		autoTitle = series
	} else {
		t := d.Title
		if truthy(rec["title"]) {
			t = pyStr(rec["title"])
		}
		autoTitle = filmName(t)
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
		if q := pyStrip(collapseSpace(yearParensRE.ReplaceAllString(query, " "))); q != "" {
			query = q
		}
		if name, s, ok := parseSeason(query); ok {
			query, season = name, s
		} else {
			query = filmName(query)
		}
	}
	t := searchTermsT{Title: query, Year: year, Season: season, DiscSeason: discSeasonNo, AutoTitle: autoTitle}
	if query == "" {
		t.Title = autoTitle
		t.TVYear = firstTruthy(rec["year"], year)
		if !truthy(year) && rec["year"] == nil {
			t.TVYear = nil
		}
	} else {
		t.TVYear = year
	}
	return t
}

// downloadPoster saves a TMDB poster as the disc's downloaded cover. False if TMDB has no such image.
func downloadPoster(d *Disc, posterPath string) (bool, error) {
	if err := os.MkdirAll(cfg.CoverDir, 0o755); err != nil {
		return false, err
	}
	data, err := httpGet(cfg.TMDBImg+posterPath, map[string]string{"User-Agent": "streamiso/" + version}, 20*time.Second, 0)
	if err != nil {
		if h, ok := isHTTPError(err); ok { // the details are still worth keeping without the poster
			log.Printf("WARNING: Poster download failed for '%s': HTTP %d", d.Title, h.Code)
			return false, nil
		}
		return false, err
	}
	tmp := filepath.Join(cfg.CoverDir, d.ID+".jpg.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, filepath.Join(cfg.CoverDir, d.ID+".jpg"))
}

// lookupDVD looks the disc up on TMDB and stores the synopsis, genres, year and poster. A disc of
// a TV season gets that season's poster.
func lookupDVD(d *Disc, query string) (bool, error) {
	terms := searchTerms(d, query, nil)
	hit, err := tmdbLookup(terms.Title, terms.Year, terms.Season != nil, terms.TVYear)
	if err != nil {
		return false, err
	}
	if hit == nil {
		return false, storeMeta(d.ID, map[string]any{"miss": true, "season_checked": terms.DiscSeason,
			"searched": terms.AutoTitle, "checked": now()})
	}
	poster := pyStr(hit["poster_path"])
	season := terms.Season
	if season != nil && hit["tmdb_kind"] == "tv" {
		data, err := tmdbGet("/tv/"+pyStr(hit["tmdb_id"])+"/season/"+pyStr(season), lang(cfg.TMDBLanguage))
		if err != nil {
			h, ok := isHTTPError(err)
			if !ok {
				return false, err
			}
			// no such season on TMDB: keep the series poster
			log.Printf("WARNING: No season %s on TMDB for '%s': HTTP %d", pyStr(season), terms.Title, h.Code)
		} else if truthy(data["poster_path"]) {
			poster = pyStr(data["poster_path"])
			hit["season_poster"] = true
		}
	}
	if d.UserCover == "" {
		if _, err := downloadPoster(d, poster); err != nil {
			return false, err
		}
	}
	meta := copyMap(hit)
	meta["poster_path"] = poster
	ov, err := tmdbOverview(pyStr(hit["tmdb_kind"]), hit["tmdb_id"], hit["overview"])
	if err != nil {
		return false, err
	}
	meta["overview"] = ov
	meta["language"] = cfg.TMDBLanguage // the details are fetched again when it changes (see enrichAll)
	// found by the whole name searched for, not by a shorter part of it: the TMDB title can replace it
	meta["whole"] = !isPartOf(pyStr(hit["matched"]), terms.Title)
	meta["season"] = season                   // the season whose posters the poster picker shows first
	meta["season_checked"] = terms.DiscSeason // redone when the season changes (see enrichAll)
	meta["searched"] = terms.AutoTitle
	meta["checked"] = now()
	return true, storeMeta(d.ID, meta)
}

// tmdbCandidates lists the TMDB films and series with a poster that the search terms find (from
// the first query of titleVariants that finds any), series first for a season disc.
func tmdbCandidates(terms searchTermsT) ([]any, error) {
	out := []any{}
	seen := map[string]bool{}
	kinds := []string{"movie", "tv"}
	if terms.Season != nil {
		kinds = []string{"tv", "movie"}
	}
	for _, v := range titleVariants(terms.Title) {
		want := wordSet(v.query)
		for _, kind := range kinds {
			params := [][2]string{{"query", v.query}, lang(cfg.TMDBLanguage), {"include_adult", "false"}}
			if truthy(terms.Year) && kind == "movie" {
				params = append(params, [2]string{"year", pyStr(terms.Year)})
			}
			data, err := tmdbGet("/search/"+kind, params...)
			if err != nil {
				return nil, err
			}
			for _, r := range results(data) {
				if !truthy(r["poster_path"]) {
					continue
				}
				id, ok := r["id"]
				if !ok {
					return nil, fmt.Errorf("KeyError: 'id'")
				}
				key := kind + ":" + numKey(id)
				if seen[key] || (v.strict && !subset(want, wordSet(resultNames(r)))) {
					continue
				}
				seen[key] = true
				out = append(out, map[string]any{
					"tmdb_kind":   kind,
					"tmdb_id":     id,
					"title":       firstTruthy("", r["title"], r["name"]),
					"year":        resultYear(r),
					"poster_path": r["poster_path"],
				})
			}
		}
		if len(out) > 0 {
			break
		}
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out, nil
}

// tmdbPosters lists every poster TMDB holds for a film or series (the season's first, for a
// season disc), in any language: those in the catalog's language first, then English, then
// without text.
func tmdbPosters(kind string, id any, season any) ([]any, error) {
	const none = "\x00none"
	order := []string{}
	for _, l := range []string{strings.SplitN(cfg.TMDBLanguage, "-", 2)[0], "en", none} {
		dup := false
		for _, o := range order {
			dup = dup || o == l
		}
		if !dup {
			order = append(order, l)
		}
	}
	rank := func(p map[string]any) int {
		l := none
		if v, ok := p["iso_639_1"]; ok && v != nil {
			l = pyStr(v)
		}
		for i, o := range order {
			if o == l {
				return i
			}
		}
		return len(order)
	}
	sources := []string{"/" + kind + "/" + pyStr(id) + "/images"}
	if kind == "tv" && season != nil {
		sources = append([]string{"/tv/" + pyStr(id) + "/season/" + pyStr(season) + "/images"}, sources...)
	}
	paths := []any{}
	have := map[string]bool{}
	for _, src := range sources {
		data, err := tmdbGet(src) // no language asked: TMDB sends them all
		if err != nil {
			if h, ok := isHTTPError(err); ok { // e.g. no such season on TMDB
				log.Printf("WARNING: No posters at TMDB %s: HTTP %d", src, h.Code)
				continue
			}
			return nil, err
		}
		list, _ := data["posters"].([]any)
		posters := make([]map[string]any, 0, len(list))
		for _, p := range list {
			if m, ok := p.(map[string]any); ok {
				posters = append(posters, m)
			}
		}
		// stable, so TMDB's own order (by votes) stays within each language
		sort.SliceStable(posters, func(i, j int) bool { return rank(posters[i]) < rank(posters[j]) })
		var add []string
		for _, p := range posters {
			if truthy(p["file_path"]) {
				fp := pyStr(p["file_path"])
				if !have[fp] { // as in Python, only the paths of earlier sources count as seen
					add = append(add, fp)
				}
			}
		}
		for _, fp := range add {
			paths = append(paths, fp)
			have[fp] = true
		}
	}
	if len(paths) > 80 {
		paths = paths[:80]
	}
	return paths, nil
}

// tmdbDetails gives the details lookupDVD keeps, for a film or series picked by hand.
func tmdbDetails(kind string, id any) (map[string]any, error) {
	r, err := tmdbGet("/"+kind+"/"+pyStr(id), lang(cfg.TMDBLanguage))
	if err != nil {
		return nil, err
	}
	ov, err := tmdbOverview(kind, id, firstTruthy("", r["overview"]))
	if err != nil {
		return nil, err
	}
	genres := []any{}
	list, _ := getOr(r, "genres", []any{}).([]any)
	for _, g := range list {
		if gm, ok := g.(map[string]any); ok && truthy(gm["name"]) {
			genres = append(genres, gm["name"])
		}
	}
	return map[string]any{
		"tmdb_kind":  kind,
		"tmdb_id":    id,
		"overview":   ov,
		"language":   cfg.TMDBLanguage,
		"tmdb_title": firstTruthy("", r["title"], r["name"]),
		"tmdb_year":  resultYear(r),
		"rating":     r["vote_average"],
		"genres":     genres,
	}, nil
}

// lookupCD looks an audio CD up on MusicBrainz and the Cover Art Archive.
func lookupCD(d *Disc, query string) (bool, error) {
	title := query
	if title == "" {
		title = d.Title
	}
	hit, err := lookupAlbum(title, d.Artist)
	if err != nil {
		return false, err
	}
	time.Sleep(mbDelay)
	if hit == nil {
		return false, storeMeta(d.ID, map[string]any{"miss": true, "checked": now()})
	}
	gotCover := d.UserCover != ""
	if !gotCover {
		image, err := fetchCover(hit.MBID)
		if err != nil {
			return false, err
		}
		if image != nil {
			gotCover = true
			if err := os.MkdirAll(cfg.CoverDir, 0o755); err != nil {
				return false, err
			}
			if err := os.WriteFile(filepath.Join(cfg.CoverDir, d.ID+".jpg"), image, 0o644); err != nil {
				return false, err
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	genres := make([]any, len(hit.Genres))
	for i, g := range hit.Genres {
		genres[i] = g
	}
	if err := storeMeta(d.ID, map[string]any{"source": "musicbrainz", "mbid": hit.MBID, "year": hit.Year,
		"genres": genres, "overview": "", "checked": now()}); err != nil {
		return false, err
	}
	d.mu.Lock()
	d.coverFound = &gotCover
	d.mu.Unlock()
	return true, nil
}

func lookup(d *Disc, query string) (bool, error) {
	if d.Kind == "cd" {
		return lookupCD(d, query)
	}
	return lookupDVD(d, query)
}

// refreshDetails fetches the synopsis, genres, title and rating of the disc's TMDB match again,
// in TMDB_LANGUAGE (the year stays as matched).
func refreshDetails(id string, meta map[string]any) error {
	details, err := tmdbDetails(pyStr(meta["tmdb_kind"]), meta["tmdb_id"])
	if err != nil {
		return err
	}
	delete(details, "tmdb_year")
	out := copyMap(meta)
	for k, v := range details {
		out[k] = v
	}
	return storeMeta(id, out)
}

// enrichSem is the lookup lock: one lookup at a time, background or asked for.
var enrichSem = make(chan struct{}, 1)

func acquireEnrich(timeout time.Duration) bool {
	if timeout == 0 {
		select {
		case enrichSem <- struct{}{}:
			return true
		default:
			return false
		}
	}
	select {
	case enrichSem <- struct{}{}:
		return true
	case <-time.After(timeout):
		return false
	}
}

func releaseEnrich() { <-enrichSem }

// enrichAll looks up covers and details for the discs not looked up yet (in the background).
func enrichAll() {
	if !acquireEnrich(0) {
		return
	}
	defer releaseEnrich()
	lib := loadLibrary()
	for _, d := range currentDiscs() {
		isCD := d.Kind == "cd"
		if !isCD && cfg.TMDBKey == "" {
			continue // DVDs need a TMDB key
		}
		if isFile(filepath.Join(cfg.MetaDir, d.ID+".json")) || exists(filepath.Join(cfg.MetaDir, d.ID+".json")) {
			old := readMeta(d.ID)
			// Already looked up; DVD lookups are redone when they predate genres or when the
			// disc's season changed (edited or renamed), so the cover is that season's poster.
			// A miss is also redone when the title it searches for changed.
			rec := libRec(lib, d.FP)
			skip := isCD
			if !skip {
				_, hasGenres := old["genres"]
				_, season := discSeason(d, rec)
				skip = (truthy(old["miss"]) || hasGenres) && pyEqual(old["season_checked"], season) &&
					!(truthy(old["miss"]) && !pyEqual(old["searched"], searchTerms(d, "", rec).AutoTitle))
			}
			if skip {
				// Details fetched in another language (or before the language was kept) are
				// fetched again in TMDB_LANGUAGE, keeping the match and the poster.
				if !isCD && truthy(old["tmdb_id"]) && !pyEqual(old["language"], cfg.TMDBLanguage) {
					if err := refreshDetails(d.ID, old); err != nil {
						log.Printf("WARNING: Details failed for '%s': %v", d.Title, err)
						time.Sleep(2 * time.Second)
					}
					time.Sleep(300 * time.Millisecond)
				}
				continue
			}
		}
		if _, err := lookup(d, ""); err != nil {
			log.Printf("WARNING: Lookup failed for '%s': %v", d.Title, err)
			time.Sleep(2 * time.Second) // offline / invalid key: try again on the next scan
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
