package app

// Metadata: covers and details from TMDB for DVDs (needs a key) and from MusicBrainz for CDs, the genre
// names in TMDB_LANGUAGE, and the lookups of new discs in the background.

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"streamiso/internal/ports"
)

const fallbackLanguage = "en-US"

type MetadataConfig struct {
	TMDBKey      string // DVDs are only looked up with a key
	TMDBLanguage string
}

type Metadata struct {
	cfg     MetadataConfig
	movies  ports.MovieDB
	music   ports.MusicDB
	meta    ports.Metadata
	lib     ports.Library
	covers  ports.Covers
	catalog *Catalog

	genreMu    sync.Mutex
	genreCache map[string]map[string]string

	aliasMu      sync.Mutex
	aliasLoaded  bool
	genreAliasDB map[string]string

	enrichSem chan struct{} // one lookup at a time, background or asked for
}

func NewMetadata(cfg MetadataConfig, movies ports.MovieDB, music ports.MusicDB, meta ports.Metadata,
	lib ports.Library, covers ports.Covers, catalog *Catalog) *Metadata {
	return &Metadata{cfg: cfg, movies: movies, music: music, meta: meta, lib: lib, covers: covers, catalog: catalog,
		genreCache: map[string]map[string]string{}, genreAliasDB: map[string]string{}, enrichSem: make(chan struct{}, 1)}
}

// TMDB tells whether DVDs can be looked up (a TMDB key is set).
func (m *Metadata) TMDB() bool { return m.cfg.TMDBKey != "" }

func lang(l string) [2]string { return [2]string{"language", l} }

// numKey is the text of a JSON number as a map key (TMDB ids).
func numKey(v any) string {
	if f, ok := compat.ToFloat(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return compat.Str(v)
}

func isHTTPError(err error) (*ports.HTTPError, bool) {
	var h *ports.HTTPError
	ok := errors.As(err, &h)
	return h, ok
}

// ---------------------------------------------------------------------------------- genres

func (m *Metadata) tmdbGenreMap(kind string) (map[string]string, error) {
	m.genreMu.Lock()
	defer m.genreMu.Unlock()
	if g, ok := m.genreCache[kind]; ok {
		return g, nil
	}
	data, err := m.movies.Get("/genre/"+kind+"/list", lang(m.cfg.TMDBLanguage))
	if err != nil {
		return nil, err
	}
	g := map[string]string{}
	list, _ := data["genres"].([]any)
	for _, item := range list {
		gm, _ := item.(map[string]any)
		id, okID := gm["id"]
		name, okName := gm["name"]
		if !okID || !okName {
			return nil, fmt.Errorf("KeyError")
		}
		g[numKey(id)] = compat.Str(name)
	}
	m.genreCache[kind] = g
	return g, nil
}

// loadGenreAliases fetches the English name of each TMDB genre and its name in TMDB_LANGUAGE
// ("Documentary" and "Documentário"), so the genre tags show one of them. Once, in the background.
func (m *Metadata) loadGenreAliases() error {
	m.aliasMu.Lock()
	done := m.aliasLoaded
	m.aliasMu.Unlock()
	if done || m.cfg.TMDBKey == "" || m.cfg.TMDBLanguage == fallbackLanguage {
		return nil
	}
	aliases := map[string]string{}
	for _, kind := range []string{"movie", "tv"} {
		local, err := m.tmdbGenreMap(kind)
		if err != nil {
			return err
		}
		data, err := m.movies.Get("/genre/"+kind+"/list", lang(fallbackLanguage))
		if err != nil {
			return err
		}
		list, _ := data["genres"].([]any)
		for _, g := range list {
			gm, _ := g.(map[string]any)
			id, okID := gm["id"]
			en, okName := gm["name"]
			if !okID || !okName {
				return fmt.Errorf("KeyError")
			}
			if name, ok := local[numKey(id)]; ok && name != "" && name != compat.Str(en) {
				aliases[compat.Str(en)] = name
			}
		}
	}
	m.aliasMu.Lock()
	for k, v := range aliases {
		m.genreAliasDB[k] = v
	}
	m.aliasLoaded = true
	m.aliasMu.Unlock()
	return nil
}

// GenreAliases maps an English genre name to its name in TMDB_LANGUAGE, for the genre tags.
func (m *Metadata) GenreAliases() map[string]string {
	if m.cfg.TMDBLanguage == fallbackLanguage {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, v := range domain.LocalGenreNames[strings.SplitN(m.cfg.TMDBLanguage, "-", 2)[0]] {
		out[k] = v
	}
	m.aliasMu.Lock()
	for k, v := range m.genreAliasDB {
		out[k] = v
	}
	m.aliasMu.Unlock()
	return out
}

// ---------------------------------------------------------------------------------- TMDB answers

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

func resultNames(r map[string]any) string {
	var parts []string
	for _, k := range []string{"title", "name", "original_title", "original_name"} {
		if compat.Truthy(r[k]) {
			parts = append(parts, compat.Str(r[k]))
		}
	}
	return strings.Join(parts, " ")
}

func resultYear(r map[string]any) string {
	return compat.Slice(compat.Str(compat.FirstTruthy("", r["release_date"], r["first_air_date"])), 4)
}

// SearchTerms is what an automatic lookup of the disc searches for, or a manual one for query.
func (m *Metadata) SearchTerms(d *domain.Disc, query string) domain.SearchTerms {
	return domain.NewSearchTerms(d, query, libRec(m.lib.Load(), d.FP))
}

// tmdbLookup finds the disc's film or series. year filters films; tvYear only puts first a
// series that started that year (a revival next to the original), since a season disc's year is
// often the season's, not the series'.
func (m *Metadata) tmdbLookup(title string, year any, preferTV bool, tvYear any) (map[string]any, error) {
	kinds := []string{"movie", "tv"}
	if preferTV {
		kinds = []string{"tv", "movie"}
	}
	for _, v := range domain.TitleVariants(title) {
		want := domain.WordSet(v.Query)
		for _, kind := range kinds {
			params := [][2]string{{"query", v.Query}, lang(m.cfg.TMDBLanguage), {"include_adult", "false"}}
			if compat.Truthy(year) && kind == "movie" {
				params = append(params, [2]string{"year", compat.Str(year)})
			}
			data, err := m.movies.Get("/search/"+kind, params...)
			if err != nil {
				return nil, err
			}
			res := results(data)
			if compat.Truthy(tvYear) && kind == "tv" {
				ty := compat.Str(tvYear)
				sort.SliceStable(res, func(i, j int) bool {
					a := compat.Slice(compat.Str(compat.FirstTruthy("", res[i]["first_air_date"])), 4) != ty
					b := compat.Slice(compat.Str(compat.FirstTruthy("", res[j]["first_air_date"])), 4) != ty
					return !a && b
				})
			}
			for _, r := range res {
				if !compat.Truthy(r["poster_path"]) {
					continue
				}
				if v.Strict && !domain.Subset(want, domain.WordSet(resultNames(r))) {
					continue
				}
				gmap, err := m.tmdbGenreMap(kind)
				if err != nil {
					return nil, err
				}
				id, ok := r["id"]
				if !ok {
					return nil, fmt.Errorf("KeyError: 'id'")
				}
				genres := []any{}
				ids, _ := compat.GetOr(r, "genre_ids", []any{}).([]any)
				for _, gid := range ids {
					if name, ok := gmap[numKey(gid)]; ok {
						genres = append(genres, name)
					}
				}
				return map[string]any{
					"poster_path": r["poster_path"],
					"tmdb_kind":   kind,
					"tmdb_id":     id,
					"overview":    compat.GetOr(r, "overview", ""),
					"tmdb_title":  compat.FirstTruthy("", r["title"], r["name"]),
					"tmdb_year":   resultYear(r),
					"rating":      r["vote_average"],
					"genres":      genres,
					"matched":     v.Query,
				}, nil
			}
		}
	}
	return nil, nil
}

// tmdbOverview is the synopsis in TMDB_LANGUAGE, or else in English: TMDB sends an empty one for
// a language the title has no translation in.
func (m *Metadata) tmdbOverview(kind string, id any, overview any) (any, error) {
	if compat.Truthy(overview) || m.cfg.TMDBLanguage == fallbackLanguage {
		return overview, nil
	}
	data, err := m.movies.Get("/"+kind+"/"+compat.Str(id), lang(fallbackLanguage))
	if err != nil {
		return nil, err
	}
	return compat.FirstTruthy("", data["overview"]), nil
}

// downloadPoster saves a TMDB poster as the disc's downloaded cover. False if TMDB has no such image.
func (m *Metadata) downloadPoster(d *domain.Disc, posterPath string) (bool, error) {
	data, err := m.movies.Image(posterPath)
	if err != nil {
		if h, ok := isHTTPError(err); ok { // the details are still worth keeping without the poster
			log.Printf("WARNING: Poster download failed for '%s': HTTP %d", d.Title, h.Code)
			return false, nil
		}
		return false, err
	}
	return true, m.covers.SaveDownloaded(d.ID, data)
}

// lookupDVD looks the disc up on TMDB and stores the synopsis, genres, year and poster. A disc of
// a TV season gets that season's poster.
func (m *Metadata) lookupDVD(d *domain.Disc, query string) (bool, error) {
	terms := m.SearchTerms(d, query)
	hit, err := m.tmdbLookup(terms.Title, terms.Year, terms.Season != nil, terms.TVYear)
	if err != nil {
		return false, err
	}
	if hit == nil {
		return false, m.meta.Store(d.ID, map[string]any{"miss": true, "season_checked": terms.DiscSeason,
			"searched": terms.AutoTitle, "checked": now()})
	}
	poster := compat.Str(hit["poster_path"])
	season := terms.Season
	if season != nil && hit["tmdb_kind"] == "tv" {
		data, err := m.movies.Get("/tv/"+compat.Str(hit["tmdb_id"])+"/season/"+compat.Str(season), lang(m.cfg.TMDBLanguage))
		if err != nil {
			h, ok := isHTTPError(err)
			if !ok {
				return false, err
			}
			// no such season on TMDB: keep the series poster
			log.Printf("WARNING: No season %s on TMDB for '%s': HTTP %d", compat.Str(season), terms.Title, h.Code)
		} else if compat.Truthy(data["poster_path"]) {
			poster = compat.Str(data["poster_path"])
			hit["season_poster"] = true
		}
	}
	if d.UserCover == "" {
		if _, err := m.downloadPoster(d, poster); err != nil {
			return false, err
		}
	}
	meta := compat.CopyMap(hit)
	meta["poster_path"] = poster
	ov, err := m.tmdbOverview(compat.Str(hit["tmdb_kind"]), hit["tmdb_id"], hit["overview"])
	if err != nil {
		return false, err
	}
	meta["overview"] = ov
	meta["language"] = m.cfg.TMDBLanguage // the details are fetched again when it changes (see EnrichAll)
	// found by the whole name searched for, not by a shorter part of it: the TMDB title can replace it
	meta["whole"] = !domain.IsPartOf(compat.Str(hit["matched"]), terms.Title)
	meta["season"] = season                   // the season whose posters the poster picker shows first
	meta["season_checked"] = terms.DiscSeason // redone when the season changes (see EnrichAll)
	meta["searched"] = terms.AutoTitle
	meta["checked"] = now()
	return true, m.meta.Store(d.ID, meta)
}

// tmdbCandidates lists the TMDB films and series with a poster that the search terms find (from
// the first query of TitleVariants that finds any), series first for a season disc.
func (m *Metadata) tmdbCandidates(terms domain.SearchTerms) ([]any, error) {
	out := []any{}
	seen := map[string]bool{}
	kinds := []string{"movie", "tv"}
	if terms.Season != nil {
		kinds = []string{"tv", "movie"}
	}
	for _, v := range domain.TitleVariants(terms.Title) {
		want := domain.WordSet(v.Query)
		for _, kind := range kinds {
			params := [][2]string{{"query", v.Query}, lang(m.cfg.TMDBLanguage), {"include_adult", "false"}}
			if compat.Truthy(terms.Year) && kind == "movie" {
				params = append(params, [2]string{"year", compat.Str(terms.Year)})
			}
			data, err := m.movies.Get("/search/"+kind, params...)
			if err != nil {
				return nil, err
			}
			for _, r := range results(data) {
				if !compat.Truthy(r["poster_path"]) {
					continue
				}
				id, ok := r["id"]
				if !ok {
					return nil, fmt.Errorf("KeyError: 'id'")
				}
				key := kind + ":" + numKey(id)
				if seen[key] || (v.Strict && !domain.Subset(want, domain.WordSet(resultNames(r)))) {
					continue
				}
				seen[key] = true
				out = append(out, map[string]any{
					"tmdb_kind":   kind,
					"tmdb_id":     id,
					"title":       compat.FirstTruthy("", r["title"], r["name"]),
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
func (m *Metadata) tmdbPosters(kind string, id any, season any) ([]any, error) {
	const none = "\x00none"
	order := []string{}
	for _, l := range []string{strings.SplitN(m.cfg.TMDBLanguage, "-", 2)[0], "en", none} {
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
			l = compat.Str(v)
		}
		for i, o := range order {
			if o == l {
				return i
			}
		}
		return len(order)
	}
	sources := []string{"/" + kind + "/" + compat.Str(id) + "/images"}
	if kind == "tv" && season != nil {
		sources = append([]string{"/tv/" + compat.Str(id) + "/season/" + compat.Str(season) + "/images"}, sources...)
	}
	paths := []any{}
	have := map[string]bool{}
	for _, src := range sources {
		data, err := m.movies.Get(src) // no language asked: TMDB sends them all
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
			if pm, ok := p.(map[string]any); ok {
				posters = append(posters, pm)
			}
		}
		// stable, so TMDB's own order (by votes) stays within each language
		sort.SliceStable(posters, func(i, j int) bool { return rank(posters[i]) < rank(posters[j]) })
		var add []string
		for _, p := range posters {
			if compat.Truthy(p["file_path"]) {
				fp := compat.Str(p["file_path"])
				if !have[fp] { // only the paths of earlier sources count as seen
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
func (m *Metadata) tmdbDetails(kind string, id any) (map[string]any, error) {
	r, err := m.movies.Get("/"+kind+"/"+compat.Str(id), lang(m.cfg.TMDBLanguage))
	if err != nil {
		return nil, err
	}
	ov, err := m.tmdbOverview(kind, id, compat.FirstTruthy("", r["overview"]))
	if err != nil {
		return nil, err
	}
	genres := []any{}
	list, _ := compat.GetOr(r, "genres", []any{}).([]any)
	for _, g := range list {
		if gm, ok := g.(map[string]any); ok && compat.Truthy(gm["name"]) {
			genres = append(genres, gm["name"])
		}
	}
	return map[string]any{
		"tmdb_kind":  kind,
		"tmdb_id":    id,
		"overview":   ov,
		"language":   m.cfg.TMDBLanguage,
		"tmdb_title": compat.FirstTruthy("", r["title"], r["name"]),
		"tmdb_year":  resultYear(r),
		"rating":     r["vote_average"],
		"genres":     genres,
	}, nil
}

// ---------------------------------------------------------------------------------- CDs

// lookupCD looks an audio CD up on MusicBrainz and the Cover Art Archive.
func (m *Metadata) lookupCD(d *domain.Disc, query string) (bool, error) {
	title := query
	if title == "" {
		title = d.Title
	}
	hit, err := m.music.LookupAlbum(title, d.Artist)
	if err != nil {
		return false, err
	}
	m.music.Pause()
	if hit == nil {
		return false, m.meta.Store(d.ID, map[string]any{"miss": true, "checked": now()})
	}
	gotCover := d.UserCover != ""
	if !gotCover {
		image, err := m.music.FetchCover(hit.MBID)
		if err != nil {
			return false, err
		}
		if image != nil {
			gotCover = true
			if err := m.covers.SaveDownloaded(d.ID, image); err != nil {
				return false, err
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	genres := make([]any, len(hit.Genres))
	for i, g := range hit.Genres {
		genres[i] = g
	}
	if err := m.meta.Store(d.ID, map[string]any{"source": "musicbrainz", "mbid": hit.MBID, "year": hit.Year,
		"genres": genres, "overview": "", "checked": now()}); err != nil {
		return false, err
	}
	d.SetCoverFound(gotCover)
	return true, nil
}

func (m *Metadata) lookup(d *domain.Disc, query string) (bool, error) {
	if d.Kind == "cd" {
		return m.lookupCD(d, query)
	}
	return m.lookupDVD(d, query)
}

// ---------------------------------------------------------------------------------- the use cases

func (m *Metadata) acquire(timeout time.Duration) bool {
	if timeout == 0 {
		select {
		case m.enrichSem <- struct{}{}:
			return true
		default:
			return false
		}
	}
	select {
	case m.enrichSem <- struct{}{}:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (m *Metadata) release() { <-m.enrichSem }

var errBusy = fail(503, "A background lookup is still running, try again in a minute")

// Lookup looks a disc up again, by its own title or by query. It gives whether something was found
// and, for a CD, whether a cover was.
func (m *Metadata) Lookup(d *domain.Disc, query string) (found, cover bool, err error) {
	if d.Kind != "cd" && m.cfg.TMDBKey == "" {
		return false, false, fail(400, "Set TMDB_API_KEY in the .env file to search for DVDs")
	}
	if !m.acquire(60 * time.Second) {
		return false, false, errBusy
	}
	found, err = m.lookup(d, query)
	m.release()
	if err != nil {
		return false, false, fail(502, "Lookup failed: "+ErrText(err))
	}
	cover = true
	if found {
		if c, ok := d.TakeCoverFound(); ok {
			cover = c
		}
	}
	return found, cover, nil
}

// CheckPosters fails for a disc that cannot have TMDB posters (a CD, or no TMDB key).
func (m *Metadata) CheckPosters(d *domain.Disc) error {
	if d.Kind == "cd" {
		return fail(400, "Posters come from TMDB, which only has films and series")
	}
	if m.cfg.TMDBKey == "" {
		return fail(400, "Set TMDB_API_KEY in the .env file to search for DVDs")
	}
	return nil
}

// TitlePosters lists the posters of one film or series, for the poster picker.
func (m *Metadata) TitlePosters(d *domain.Disc, kind string, id int64) ([]any, error) {
	if err := m.CheckPosters(d); err != nil {
		return nil, err
	}
	var season any
	if kind == "tv" {
		season = m.SearchTerms(d, "").DiscSeason
	}
	posters, err := m.tmdbPosters(kind, id, season)
	if err != nil {
		return nil, fail(502, "Search failed: "+ErrText(err))
	}
	return posters, nil
}

// PosterChoices is what the poster picker shows: the films and series query finds (or the disc's
// title), and the current match with its posters.
func (m *Metadata) PosterChoices(d *domain.Disc, query string) (map[string]any, error) {
	if err := m.CheckPosters(d); err != nil {
		return nil, err
	}
	meta := m.meta.Read(d.ID)
	terms := m.SearchTerms(d, query)
	matches, err := m.tmdbCandidates(terms)
	if err != nil {
		return nil, fail(502, "Search failed: "+ErrText(err))
	}
	var current any
	mk, _ := meta["tmdb_kind"].(string)
	if (mk == "tv" || mk == "movie") && compat.Truthy(meta["tmdb_id"]) {
		var season any
		if mk == "tv" {
			season = compat.GetOr(meta, "season", terms.DiscSeason)
		}
		posters, err := m.tmdbPosters(mk, meta["tmdb_id"], season)
		if err != nil {
			return nil, fail(502, "Search failed: "+ErrText(err))
		}
		current = map[string]any{
			"tmdb_kind":   mk,
			"tmdb_id":     meta["tmdb_id"],
			"title":       compat.GetOr(meta, "tmdb_title", ""),
			"year":        compat.GetOr(meta, "tmdb_year", ""),
			"season":      season,
			"poster_path": meta["poster_path"],
			"posters":     posters,
		}
	}
	return map[string]any{"query": terms.Title, "current": current, "matches": matches}, nil
}

var posterPathRE = regexp.MustCompile(`^/[A-Za-z0-9_-]{1,100}\.(?:jpe?g|png|webp)$`)

// SetPoster makes a TMDB poster the disc's cover, and that poster's film or series its match.
func (m *Metadata) SetPoster(d *domain.Disc, payload map[string]any) error {
	if err := m.CheckPosters(d); err != nil {
		return err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	pathV, pathOK := payload["poster_path"].(string)
	kind, _ := payload["tmdb_kind"].(string)
	tmdbID := payload["tmdb_id"]
	idNum, idOK := tmdbID.(interface{ Float64() (float64, error) })
	idValid := idOK && compat.IsInt(tmdbID)
	if idValid {
		f, _ := idNum.Float64()
		idValid = f > 0
	}
	if !pathOK || !posterPathRE.MatchString(pathV) || (kind != "tv" && kind != "movie") || !idValid {
		return fail(400, "Invalid poster")
	}
	if !m.acquire(60 * time.Second) {
		return errBusy
	}
	meta, terms, err := func() (map[string]any, domain.SearchTerms, error) {
		defer m.release()
		meta := m.meta.Read(d.ID)
		terms := m.SearchTerms(d, "")
		if !compat.Equal(meta["tmdb_kind"], kind) || !compat.Equal(meta["tmdb_id"], tmdbID) {
			details, err := m.tmdbDetails(kind, tmdbID)
			if err != nil {
				return nil, terms, fail(502, "Could not get the poster: "+ErrText(err))
			}
			meta = details
			meta["season"] = nil
			if kind == "tv" {
				meta["season"] = terms.DiscSeason
			}
			meta["matched"] = "(chosen by hand)"
			meta["whole"] = true
		}
		got, err := m.downloadPoster(d, pathV)
		if err != nil {
			return nil, terms, fail(502, "Could not get the poster: "+ErrText(err))
		}
		if !got {
			return nil, terms, fail(502, "TMDB could not send that poster")
		}
		return meta, terms, nil
	}()
	if err != nil {
		return err
	}
	delete(meta, "miss")
	meta["poster_path"] = pathV
	meta["poster_chosen"] = true
	meta["season_checked"] = terms.DiscSeason
	meta["searched"] = terms.AutoTitle
	meta["checked"] = now()
	return m.meta.Store(d.ID, meta)
}

// refreshDetails fetches the synopsis, genres, title and rating of the disc's TMDB match again,
// in TMDB_LANGUAGE (the year stays as matched).
func (m *Metadata) refreshDetails(id string, meta map[string]any) error {
	details, err := m.tmdbDetails(compat.Str(meta["tmdb_kind"]), meta["tmdb_id"])
	if err != nil {
		return err
	}
	delete(details, "tmdb_year")
	out := compat.CopyMap(meta)
	for k, v := range details {
		out[k] = v
	}
	return m.meta.Store(id, out)
}

// EnrichAll looks up covers and details for the discs not looked up yet (in the background).
func (m *Metadata) EnrichAll() {
	if !m.acquire(0) {
		return
	}
	defer m.release()
	if err := m.loadGenreAliases(); err != nil {
		log.Printf("WARNING: Genre names failed: %v", err) // tried again on the next scan
	}
	lib := m.lib.Load()
	for _, d := range m.catalog.Current() {
		isCD := d.Kind == "cd"
		if !isCD && m.cfg.TMDBKey == "" {
			continue // DVDs need a TMDB key
		}
		if m.meta.Has(d.ID) {
			old := m.meta.Read(d.ID)
			// Already looked up; DVD lookups are redone when they predate genres or when the
			// disc's season changed (edited or renamed), so the cover is that season's poster.
			// A miss is also redone when the title it searches for changed.
			rec := libRec(lib, d.FP)
			skip := isCD
			if !skip {
				_, hasGenres := old["genres"]
				_, season := domain.DiscSeason(d, rec)
				skip = (compat.Truthy(old["miss"]) || hasGenres) && compat.Equal(old["season_checked"], season) &&
					!(compat.Truthy(old["miss"]) && !compat.Equal(old["searched"], domain.NewSearchTerms(d, "", rec).AutoTitle))
			}
			if skip {
				// Details fetched in another language (or before the language was kept) are
				// fetched again in TMDB_LANGUAGE, keeping the match and the poster.
				if !isCD && compat.Truthy(old["tmdb_id"]) && !compat.Equal(old["language"], m.cfg.TMDBLanguage) {
					if err := m.refreshDetails(d.ID, old); err != nil {
						log.Printf("WARNING: Details failed for '%s': %v", d.Title, err)
						time.Sleep(2 * time.Second)
					}
					time.Sleep(300 * time.Millisecond)
				}
				continue
			}
		}
		if _, err := m.lookup(d, ""); err != nil {
			log.Printf("WARNING: Lookup failed for '%s': %v", d.Title, err)
			time.Sleep(2 * time.Second) // offline / invalid key: try again on the next scan
		}
		time.Sleep(300 * time.Millisecond)
	}
}
