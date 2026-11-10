package app

// Library: the discs as the user sees them (their edits over what was found), and the changes the user
// makes: edits, watched, covers, forgetting where a disc was left.

import (
	"math"
	"sort"
	"strconv"

	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"streamiso/internal/ports"
)

type Library struct {
	lib      ports.Library
	meta     ports.Metadata
	resume   ports.Resume
	covers   ports.Covers
	state    ports.PlayerState
	catalog  *Catalog
	metadata *Metadata
}

func NewLibrary(lib ports.Library, meta ports.Metadata, resume ports.Resume, covers ports.Covers,
	state ports.PlayerState, catalog *Catalog, metadata *Metadata) *Library {
	return &Library{lib: lib, meta: meta, resume: resume, covers: covers, state: state, catalog: catalog, metadata: metadata}
}

func yearAuto(d *domain.Disc, meta map[string]any) any {
	if d.Year != "" {
		if n, err := strconv.Atoi(d.Year); err == nil {
			return n
		}
	}
	if s := compat.Str(compat.FirstTruthy("", meta["tmdb_year"], meta["year"])); compat.IsAllDigits(s) {
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
	return nil
}

// resumeView is where the disc was left: time, percent and, for a CD, the track.
func (l *Library) resumeView(d *domain.Disc) any {
	entry := l.resume.Get(d.FP)
	if len(entry) == 0 {
		return nil
	}
	pos := compat.Int(compat.GetOr(entry, "time", int64(0)))
	var length float64
	if n, ok := compat.ToFloat(entry["length"]); ok && n != 0 {
		length = n
	} else if d.Duration != nil && *d.Duration != 0 {
		length = math.Trunc(*d.Duration)
	}
	out := map[string]any{"time": pos, "pct": nil}
	if length != 0 {
		out["pct"] = compat.Round(100 * float64(pos) / length)
	}
	if d.Kind == "cd" {
		idx := domain.Locate(d.Tracks, pos)
		out["track"] = idx + 1
		tt := pos - int64(d.Tracks[idx].Start)
		if tt < 0 {
			tt = 0
		}
		out["track_time"] = tt
	}
	return out
}

// ShownTitle is the title the user sees: their own, or the one found.
func (l *Library) ShownTitle(d *domain.Disc) any {
	rec := libRec(l.lib.Load(), d.FP)
	if compat.Truthy(rec["title"]) {
		return rec["title"]
	}
	return domain.LocalizedTitle(d, l.meta.Read(d.ID))
}

// DiscView is a disc as the UI sees it: the user's edits layered over the automatic values.
func (l *Library) DiscView(d *domain.Disc, lib map[string]any) map[string]any {
	meta := l.meta.Read(d.ID)
	ov := libRec(lib, d.FP)
	var genres any = compat.GetOr(meta, "genres", []any{})
	if len(d.GenresAuto) > 0 {
		genres = d.GenresAuto
	}
	aliases := l.metadata.GenreAliases()
	auto := map[string]any{
		"title":    domain.LocalizedTitle(d, meta),
		"year":     yearAuto(d, meta),
		"genres":   domain.GenreTags(genres, aliases),
		"synopsis": compat.GetOr(meta, "overview", ""),
	}
	year := auto["year"]
	if v, ok := ov["year"]; ok && v != nil {
		year = v
	}
	coverFill := true // filling is the default
	if b, ok := ov["cover_fill"].(bool); ok && !b {
		coverFill = false
	}
	var series any
	if meta["tmdb_kind"] == "tv" {
		series = meta["tmdb_id"]
	}
	var duration any
	if d.Duration != nil {
		duration = *d.Duration
	}
	return map[string]any{
		"id":          d.ID,
		"kind":        d.Kind,
		"artist":      d.Artist,
		"track_count": len(d.Tracks),
		"duration":    duration,
		"title":       compat.FirstTruthy(auto["title"], ov["title"]),
		"sort_title":  compat.FirstTruthy(auto["title"], ov["sort_title"], ov["title"]),
		"year":        year,
		"genres":      compat.FirstTruthy(auto["genres"], domain.GenreTags(ov["genres"], aliases)),
		"synopsis":    compat.FirstTruthy(auto["synopsis"], ov["synopsis"]),
		"watched":     compat.Truthy(ov["watched"]),
		"cover_fill":  coverFill,
		"resume":      l.resumeView(d),
		// the TV series found on TMDB: groups the discs of a season apart from a same-named series
		"tmdb_series":  series,
		"season":       ov["season"],
		"disc":         ov["disc"],
		"rating":       meta["rating"],
		"label":        d.Label,
		"size":         d.Size,
		"rel":          d.Rel,
		"added":        d.Added,
		"cover_v":      l.covers.Version(d),
		"custom_cover": l.covers.Custom(d.FP) != "",
		"custom":       ov,
		"auto":         auto,
	}
}

// View is DiscView with the library read now.
func (l *Library) View(d *domain.Disc) map[string]any { return l.DiscView(d, l.lib.Load()) }

// Discs is the whole library, sorted by sort title, with every genre tag in use and the disc that is open.
func (l *Library) Discs() (discs []map[string]any, genres []string, aliases map[string]string, playing any) {
	lib := l.lib.Load()
	cat := l.catalog.Get(false)
	playingPath := l.state.Read()
	discs = make([]map[string]any, 0, cat.Len())
	for _, d := range cat.Values() {
		discs = append(discs, l.DiscView(d, lib))
	}
	// by sort title, the order of clients that do not sort themselves (the TV app)
	sort.SliceStable(discs, func(i, j int) bool {
		a, b := compat.Casefold(compat.Str(discs[i]["sort_title"])), compat.Casefold(compat.Str(discs[j]["sort_title"]))
		if a != b {
			return a < b
		}
		return compat.Casefold(compat.Str(discs[i]["title"])) < compat.Casefold(compat.Str(discs[j]["title"]))
	})
	// The discs' genres are already tags (see domain.GenreTags). The aliases let a client name its own
	// English genres (the editor's suggestions, or what was typed) the same way.
	aliases = l.metadata.GenreAliases()
	genreSet := map[string]bool{}
	genres = []string{}
	for _, d := range discs {
		list, _ := d["genres"].([]any)
		for _, g := range list {
			s := compat.Str(g)
			if !genreSet[s] {
				genreSet[s] = true
				genres = append(genres, s)
			}
		}
	}
	sort.SliceStable(genres, func(i, j int) bool { return compat.Casefold(genres[i]) < compat.Casefold(genres[j]) })
	for _, d := range cat.Values() {
		if d.State == playingPath {
			playing = d.ID
			break
		}
	}
	return discs, genres, aliases, playing
}

// Cover is the disc's cover: a file (with how long a browser may keep it and its type, "" to guess
// it), or else the generated placeholder.
func (l *Library) Cover(d *domain.Disc) (file string, maxAge int, kind string, placeholder string) {
	if c := l.covers.Custom(d.FP); c != "" {
		return c, 3600, "", ""
	}
	if d.UserCover != "" {
		return d.UserCover, 3600, "", ""
	}
	if c := l.covers.Downloaded(d.ID); c != "" {
		return c, 86400, "image/jpeg", ""
	}
	ov := libRec(l.lib.Load(), d.FP)
	title := compat.Str(compat.FirstTruthy(domain.LocalizedTitle(d, l.meta.Read(d.ID)), ov["title"]))
	var year any = d.Year
	if d.Year == "" {
		year = nil
	}
	return "", 0, "", domain.PlaceholderSVG(title, compat.FirstTruthy(year, ov["year"]), d.Kind == "cd")
}

// Covered tells whether the disc has a cover of the user's (uploaded, or an image next to it).
func (l *Library) Covered(d *domain.Disc) bool {
	return l.covers.Custom(d.FP) != "" || d.UserCover != ""
}

// SaveEdits replaces the user's edits of a disc and gives its new view.
func (l *Library) SaveEdits(d *domain.Disc, payload map[string]any) (map[string]any, error) {
	clean, err := domain.CleanMeta(payload)
	if err != nil {
		return nil, fail(400, err.Error())
	}
	l.lib.Lock()
	lib := l.lib.Load()
	if len(clean) > 0 {
		lib[d.FP] = clean
	} else {
		delete(lib, d.FP)
	}
	err = l.lib.Save(lib)
	l.lib.Unlock()
	if err != nil {
		return nil, err
	}
	if l.metadata.TMDB() && d.Kind != "cd" {
		go l.metadata.EnrichAll() // a new season fetches its poster
	}
	return l.DiscView(d, lib), nil
}

func (l *Library) SetWatched(d *domain.Disc, watched bool) error {
	l.lib.Lock()
	defer l.lib.Unlock()
	lib := l.lib.Load()
	rec := libRec(lib, d.FP)
	if watched {
		rec["watched"] = true
	} else {
		delete(rec, "watched")
	}
	if len(rec) > 0 {
		lib[d.FP] = rec
	} else {
		delete(lib, d.FP)
	}
	return l.lib.Save(lib)
}

// ForgetResume forgets where the disc was left, so it leaves "Continue watching".
func (l *Library) ForgetResume(d *domain.Disc) { l.resume.Set(d.FP, nil) }

// SaveCover makes an uploaded image the disc's cover.
func (l *Library) SaveCover(d *domain.Disc, data []byte) error {
	if len(data) > domain.MaxCoverBytes {
		return fail(413, "Image too large (max 8 MB)")
	}
	ext := domain.SniffImage(data)
	if ext == "" {
		return fail(400, "Unsupported format (use JPG, PNG or WebP)")
	}
	return l.covers.SaveCustom(d.FP, ext, data)
}

func (l *Library) DeleteCover(d *domain.Disc) { l.covers.DeleteCustom(d.FP) }
