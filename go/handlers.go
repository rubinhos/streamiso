package main

// The routes: the web page, the JSON API (see ../docs/API.md) and the player commands.

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxContentLength = maxCoverBytes + 256*1024

// ---------------------------------------------------------------------------------- responses
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(jsonMarshal(v, ""), '\n'))
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

var httpErrorText = map[int][2]string{
	404: {"Not Found", "The requested URL was not found on the server. If you entered the URL manually please check your spelling and try again."},
	405: {"Method Not Allowed", "The method is not allowed for the requested URL."},
	413: {"Request Entity Too Large", "The data value transmitted exceeds the capacity limit."},
	500: {"Internal Server Error", "The server encountered an internal error and was unable to complete your request. Either the server is overloaded or there is an error in the application."},
}

// htmlError is the page Flask sends for abort(status).
func htmlError(w http.ResponseWriter, status int) {
	t := httpErrorText[status]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html>\n<html lang=en>\n<title>%d %s</title>\n<h1>%s</h1>\n<p>%s</p>\n", status, t[0], t[0], t[1])
}

// serveFile sends a file like Flask's send_file: maxAge < 0 means "no-cache".
func serveFile(w http.ResponseWriter, r *http.Request, p string, maxAge int, ctype string) {
	f, err := os.Open(p)
	if err != nil {
		htmlError(w, 404)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		htmlError(w, 404)
		return
	}
	if ctype == "" {
		ctype = mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		// Werkzeug names the charset of text and XML types
		if (strings.HasPrefix(ctype, "text/") || strings.HasSuffix(ctype, "+xml")) && !strings.Contains(ctype, "charset") {
			ctype += "; charset=utf-8"
		}
	}
	w.Header().Set("Content-Type", ctype)
	if maxAge >= 0 {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
		w.Header().Set("Expires", time.Now().Add(time.Duration(maxAge)*time.Second).UTC().Format(http.TimeFormat))
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func staticFile(name string) string { return filepath.Join(cfg.StaticDir, filepath.FromSlash(name)) }

// ---------------------------------------------------------------------------------- requests
var errTooLarge = errors.New("too large")

// getJSON is Flask's request.get_json(silent=True): nil unless the body is JSON.
func getJSON(r *http.Request) (any, error) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !(ct == "application/json" || (strings.HasPrefix(ct, "application/") && strings.HasSuffix(ct, "+json"))) {
		return nil, nil
	}
	if r.ContentLength > maxContentLength {
		return nil, errTooLarge
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxContentLength))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errTooLarge
		}
		return nil, nil
	}
	v, err := decodeJSON(data)
	if err != nil {
		return nil, nil
	}
	return v, nil
}

// jsonBody reads the JSON body as an object (nil when it is not one). false means the response
// (413) was already sent.
func jsonBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	v, err := getJSON(r)
	if err == errTooLarge {
		htmlError(w, 413)
		return nil, false
	}
	m, _ := v.(map[string]any)
	return m, true
}

func getDisc(w http.ResponseWriter, r *http.Request) *Disc {
	d := getCatalog(false).byID[r.PathValue("id")]
	if d == nil {
		htmlError(w, 404)
	}
	return d
}

// ---------------------------------------------------------------------------------- the disc view
func yearAuto(d *Disc, meta map[string]any) any {
	if d.Year != "" {
		if n, err := strconv.Atoi(d.Year); err == nil {
			return n
		}
	}
	if s := pyStr(firstTruthy("", meta["tmdb_year"], meta["year"])); isAllDigits(s) {
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
	return nil
}

func resumeView(d *Disc) any {
	entry := resumeEntry(d.FP)
	if len(entry) == 0 {
		return nil
	}
	pos := pyInt(getOr(entry, "time", int64(0)))
	var length float64
	if l, ok := toFloat(entry["length"]); ok && l != 0 {
		length = l
	} else if d.Duration != nil && *d.Duration != 0 {
		length = math.Trunc(*d.Duration)
	}
	out := map[string]any{"time": pos, "pct": nil}
	if length != 0 {
		out["pct"] = pyRound(100 * float64(pos) / length)
	}
	if d.Kind == "cd" {
		idx := locate(d.Tracks, pos)
		out["track"] = idx + 1
		tt := pos - int64(d.Tracks[idx].Start)
		if tt < 0 {
			tt = 0
		}
		out["track_time"] = tt
	}
	return out
}

// pyInt is int(value) for a decoded JSON value (0 when it is not a number).
func pyInt(v any) int64 {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		n, _ := strconv.ParseInt(pyStrip(x), 10, 64)
		return n
	}
	f, _ := toFloat(v)
	return int64(f)
}

func shownTitle(d *Disc) any {
	rec := libRec(loadLibrary(), d.FP)
	if truthy(rec["title"]) {
		return rec["title"]
	}
	return localizedTitle(d, readMeta(d.ID))
}

// discView is a disc as the UI sees it: the user's edits layered over the automatic values.
func discView(d *Disc, lib map[string]any) map[string]any {
	meta := readMeta(d.ID)
	ov := libRec(lib, d.FP)
	var genres any = getOr(meta, "genres", []any{})
	if len(d.GenresAuto) > 0 {
		genres = d.GenresAuto
	}
	aliases := genreAliases()
	auto := map[string]any{
		"title":    localizedTitle(d, meta),
		"year":     yearAuto(d, meta),
		"genres":   genreTags(genres, aliases),
		"synopsis": getOr(meta, "overview", ""),
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
		"title":       firstTruthy(auto["title"], ov["title"]),
		"sort_title":  firstTruthy(auto["title"], ov["sort_title"], ov["title"]),
		"year":        year,
		"genres":      firstTruthy(auto["genres"], genreTags(ov["genres"], aliases)),
		"synopsis":    firstTruthy(auto["synopsis"], ov["synopsis"]),
		"watched":     truthy(ov["watched"]),
		"cover_fill":  coverFill,
		"resume":      resumeView(d),
		// the TV series found on TMDB: groups the discs of a season apart from a same-named series
		"tmdb_series":  series,
		"season":       ov["season"],
		"disc":         ov["disc"],
		"rating":       meta["rating"],
		"label":        d.Label,
		"size":         d.Size,
		"rel":          d.Rel,
		"added":        d.Added,
		"cover_v":      coverVersion(d),
		"custom_cover": findCustomCover(d.FP) != "",
		"custom":       ov,
		"auto":         auto,
	}
}

// ---------------------------------------------------------------------------------- edits
var intTextRE = regexp.MustCompile(`^[+-]?\d+$`)

func intOrNone(v any, lo, hi int64, what string) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok && s == "" {
		return nil, nil
	}
	bad := &ValueError{what + ": not a valid number"}
	var n int64
	switch x := v.(type) {
	case bool:
		if x {
			n = 1
		}
	case string:
		s := strings.ReplaceAll(pyStrip(x), "_", "")
		if !intTextRE.MatchString(s) || strings.Contains(pyStrip(x), "__") {
			return nil, bad
		}
		p, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, bad
		}
		n = p
	default:
		f, ok := toFloat(v)
		if !ok {
			return nil, bad
		}
		f = math.Trunc(f)
		if f < float64(lo) || f > float64(hi) {
			return nil, &ValueError{fmt.Sprintf("%s: use a value between %d and %d", what, lo, hi)}
		}
		n = int64(f)
	}
	if n < lo || n > hi {
		return nil, &ValueError{fmt.Sprintf("%s: use a value between %d and %d", what, lo, hi)}
	}
	return n, nil
}

// cleanMeta validates and normalizes the edits; empty fields are dropped (they fall back to the
// automatic value).
func cleanMeta(p map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if t := pyStrip(pyStrOr(p["title"], "")); t != "" {
		out["title"] = runeSlice(t, 200)
	}
	if t := pyStrip(pyStrOr(p["sort_title"], "")); t != "" { // sorts the disc under another name
		out["sort_title"] = runeSlice(t, 200)
	}
	year, err := intOrNone(p["year"], 1888, 2100, "Year")
	if err != nil {
		return nil, err
	}
	if year != nil {
		out["year"] = year
	}
	var list []any
	switch g := firstTruthy([]any{}, p["genres"]).(type) {
	case string:
		for _, s := range strings.Split(g, ",") {
			list = append(list, s)
		}
	case []any:
		list = g
	}
	seen := map[string]bool{}
	clean := []any{}
	for _, g := range list {
		s := runeSlice(pyStrip(pyStr(g)), 40)
		if s != "" && !seen[casefold(s)] {
			seen[casefold(s)] = true
			clean = append(clean, s)
		}
	}
	if len(clean) > 0 {
		if len(clean) > 12 {
			clean = clean[:12]
		}
		out["genres"] = clean
	}
	if s := pyStrip(pyStrOr(p["synopsis"], "")); s != "" {
		out["synopsis"] = runeSlice(s, 4000)
	}
	season, err := intOrNone(p["season"], 0, 999, "Season")
	if err != nil {
		return nil, err
	}
	if season != nil {
		out["season"] = season
	}
	disc, err := intOrNone(p["disc"], 1, 999, "Disc")
	if err != nil {
		return nil, err
	}
	if disc != nil {
		out["disc"] = disc
	}
	if truthy(p["watched"]) {
		out["watched"] = true
	}
	// Covers fill their space (cropped) unless "cover_fill": false shows this one whole. Only that
	// is stored; a payload without the field keeps the default.
	if b, ok := p["cover_fill"].(bool); ok && !b {
		out["cover_fill"] = false
	}
	return out, nil
}

// ---------------------------------------------------------------------------------- placeholder
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", ">", "&gt;", "<", "&lt;").Replace(s)
}

func placeholderSVG(title string, year any, square bool) string {
	h := 600
	if square {
		h = 400
	}
	sum := md5.Sum([]byte(title))
	hue64, _ := strconv.ParseInt(hex.EncodeToString(sum[:])[:4], 16, 64)
	hue := int(hue64 % 360)
	lines := textwrapWrap(title, 14)
	if len(lines) > 5 {
		lines = lines[:5]
	}
	if len(lines) == 0 {
		lines = []string{"DVD"}
	}
	startY := 330
	cy := 140
	if square {
		startY, cy = 250, 90
	}
	startY -= (len(lines) - 1) * 24
	var text strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&text, `<text x="200" y="%d" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="38" font-weight="700" fill="#fff">%s</text>`,
			startY+i*48, xmlEscape(line))
	}
	yearTxt := ""
	if truthy(year) {
		yearTxt = fmt.Sprintf(`<text x="200" y="%d" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="26" fill="#ffffffaa">%s</text>`,
			h-60, xmlEscape(pyStr(year)))
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 %d">`+
		`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">`+
		`<stop offset="0" stop-color="hsl(%d,45%%,32%%)"/>`+
		`<stop offset="1" stop-color="hsl(%d,50%%,14%%)"/></linearGradient></defs>`+
		`<rect width="400" height="%d" fill="url(#g)"/>`+
		`<circle cx="200" cy="%d" r="62" fill="none" stroke="#ffffff55" stroke-width="6"/>`+
		`<circle cx="200" cy="%d" r="14" fill="#ffffff55"/>`+
		`%s%s</svg>`, h, hue, (hue+40)%360, h, cy, cy, text.String(), yearTxt)
}

// ---------------------------------------------------------------------------------- playback
type resumeAt struct{ Title, Time int64 }

// pending is what the player page still has to do for the disc that was just opened. VLC opens
// paused, so the first seconds are not lost before the browser's audio and video connect.
var (
	pendMu  sync.Mutex
	pending struct {
		set    bool
		State  string
		Wait   bool
		Resume *resumeAt
	}
)

func pendingFor(d *Disc) (wait bool, res *resumeAt, ok bool) {
	pendMu.Lock()
	defer pendMu.Unlock()
	if d == nil || !pending.set || pending.State != d.State {
		return false, nil, false
	}
	return pending.Wait, pending.Resume, true
}

// rememberPosition saves the playback position (from the status poll, about once per second).
func rememberPosition(d *Disc, pos, length, title any) {
	if wait, res, ok := pendingFor(d); ok && (wait || res != nil) {
		return // still starting up: the position says nothing yet
	}
	p, ok := toFloat(pos)
	if !ok {
		return
	}
	isCD := d.Kind == "cd"
	minPos := 30.0
	if isCD {
		minPos = 10
	}
	if p < minPos {
		return
	}
	l, hasLen := toFloat(length)
	if !hasLen {
		l = 0
	}
	if !isCD && l < 300 {
		return // a menu or a short extra, nothing worth resuming
	}
	if l != 0 {
		margin := 5.0
		if !isCD {
			margin = math.Max(60, l*0.03)
		}
		if p >= l-margin {
			setResume(d.FP, nil) // reached the end: next time starts over
			return
		}
	}
	if last := resumeEntry(d.FP); len(last) > 0 {
		t, ok := toFloat(getOr(last, "time", int64(-99)))
		if ok && math.Abs(t-p) < 5 {
			return
		}
	}
	var lenOut any
	if l != 0 {
		lenOut = int64(l)
	}
	var titleOut int64
	if t, ok := toFloat(title); ok && t != 0 {
		titleOut = int64(t)
	}
	setResume(d.FP, map[string]any{"time": int64(p), "length": lenOut, "title": titleOut, "updated": now()})
}

func playingDisc() *Disc {
	state := readState()
	if state == "" {
		return nil
	}
	for _, d := range getCatalog(false).values() {
		if d.State == state {
			return d
		}
	}
	return nil
}

func nekoURL(r *http.Request) string {
	query := "usr=" + url.QueryEscape(cfg.NekoUser) + "&pwd=" + url.QueryEscape(cfg.NekoPassword) + "&cast=1"
	if cfg.NekoPublicURL != "" {
		return cfg.NekoPublicURL + "/?" + query
	}
	host := r.Host
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			host = host[1:end]
		}
	} else if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	if host == "" {
		host = "localhost"
	}
	if strings.Contains(host, ":") { // IPv6
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + cfg.NekoPort + "/?" + query
}

func cdTracksView(d *Disc) []any {
	out := make([]any, 0, len(d.Tracks))
	for _, t := range d.Tracks {
		var length any
		if t.Length != nil {
			length = pyRound2(*t.Length)
		}
		out = append(out, map[string]any{"n": t.N, "title": t.Title, "artist": t.Artist, "start": t.Start, "length": length})
	}
	return out
}

// cdStatus is the playback state of an audio CD: time and length relative to the current track.
func cdStatus(d *Disc) (map[string]any, error) {
	st, err := vlc.playback(!d.Raw)
	if err != nil {
		return nil, err
	}
	spans := d.Tracks
	if l, ok := toFloat(st["length"]); !d.Raw && ok && l != 0 { // a single audio file: the last track ends with it
		spans = trackSpans(spansTracks(spans), &l)
	}
	pos := timeOr0(st["time"])
	idx := locate(spans, pos)
	cur := spans[idx]
	start := int64(cur.Start)
	var length any
	if cur.Length != nil {
		length = pyRound(*cur.Length)
	}
	t := pos - start
	if t < 0 {
		t = 0
	}
	return map[string]any{"state": st["state"], "kind": "cd", "volume": st["volume"], "track": idx,
		"tracks": len(spans), "time": t, "length": length, "abs": pos}, nil
}

// intField reads a number from the payload, clamped to [lo, hi].
func intField(p map[string]any, key string, lo, hi int64) (int64, error) {
	v := p[key]
	if _, isBool := v.(bool); isBool {
		return 0, &ValueError{fmt.Sprintf("'%s' must be a number", key)}
	}
	f, ok := toFloat(v)
	if !ok {
		return 0, &ValueError{fmt.Sprintf("'%s' must be a number", key)}
	}
	f = math.Max(float64(lo), math.Min(float64(hi), math.Trunc(f)))
	return int64(f), nil
}

// cdCommand handles the commands that differ for audio CDs; false if the action is not one of them.
func cdCommand(d *Disc, action any, p map[string]any) (bool, error) {
	spans := d.Tracks
	a, _ := action.(string)
	switch a {
	case "seek": // seconds from the start of the current track
		t, err := vlc.getTime()
		if err != nil {
			return true, err
		}
		cur := spans[locate(spans, timeOr0(t))]
		v, err := intField(p, "value", 0, 86400)
		if err != nil {
			return true, err
		}
		return true, vlc.seek(int64(cur.Start) + v)
	case "track_next", "chapter_next":
		t, err := vlc.getTime()
		if err != nil {
			return true, err
		}
		idx := locate(spans, timeOr0(t))
		if idx+1 >= len(spans) {
			return true, &ValueError{"This is the last track"}
		}
		return true, vlc.seek(int64(spans[idx+1].Start))
	case "track_prev", "chapter_prev":
		t, err := vlc.getTime()
		if err != nil {
			return true, err
		}
		pos := timeOr0(t)
		idx := locate(spans, pos)
		// like a CD player: restart the track, or go to the previous one if it just began
		if pos-int64(spans[idx].Start) > 3 || idx == 0 {
			return true, vlc.seek(int64(spans[idx].Start))
		}
		return true, vlc.seek(int64(spans[idx-1].Start))
	case "track": // play track number (index) N
		idx, err := intField(p, "value", 0, int64(len(spans)-1))
		if err != nil {
			return true, err
		}
		return true, vlc.seek(int64(spans[idx].Start))
	}
	return false, nil
}

// applyResume jumps to the saved position (once). The disc is still paused meanwhile.
func applyResume(d *Disc) error {
	_, res, ok := pendingFor(d)
	if !ok || res == nil {
		return nil
	}
	pendMu.Lock()
	pending.Resume = nil
	pendMu.Unlock()
	if d.Kind == "cd" {
		return vlc.seek(res.Time)
	}
	for i := 0; i < 25; i++ { // wait until VLC has opened the disc
		l, err := vlc.getLength()
		if err != nil {
			return err
		}
		if timeOr0(l) > 0 {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if res.Title > 0 {
		if err := vlc.setTitle(res.Title); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
	return vlc.seek(res.Time)
}

// startPlayback lets a disc that was opened paused start playing (once).
func startPlayback(d *Disc) error {
	wait, _, ok := pendingFor(d)
	waiting := ok && wait
	st, err := vlc.playback(false)
	if err != nil {
		return err
	}
	if st["state"] == "paused" && (waiting || timeOr0(st["time"]) <= 2) {
		if err := vlc.pause(); err != nil {
			return err
		}
	}
	if waiting {
		pendMu.Lock()
		pending.Wait = false
		pendMu.Unlock()
	}
	return nil
}

// ---------------------------------------------------------------------------------- handlers
var posterPathRE = regexp.MustCompile(`^/[A-Za-z0-9_-]{1,100}\.(?:jpe?g|png|webp)$`)

func tmdbDVDError(w http.ResponseWriter, d *Disc) bool {
	if d.Kind == "cd" {
		jsonError(w, 400, "Posters come from TMDB, which only has films and series")
		return true
	}
	if cfg.TMDBKey == "" {
		jsonError(w, 400, "Set TMDB_API_KEY in the .env file to search for DVDs")
		return true
	}
	return false
}

func errText(err error) string {
	if h, ok := isHTTPError(err); ok {
		return fmt.Sprintf("HTTP Error %d: %s", h.Code, http.StatusText(h.Code))
	}
	return err.Error()
}

func handleDiscs(w http.ResponseWriter, r *http.Request) {
	lib := loadLibrary()
	cat := getCatalog(false)
	playingPath := readState()
	discs := make([]map[string]any, 0, len(cat.order))
	for _, d := range cat.values() {
		discs = append(discs, discView(d, lib))
	}
	// by sort title, the order of clients that do not sort themselves (the TV app)
	sort.SliceStable(discs, func(i, j int) bool {
		a, b := casefold(pyStr(discs[i]["sort_title"])), casefold(pyStr(discs[j]["sort_title"]))
		if a != b {
			return a < b
		}
		return casefold(pyStr(discs[i]["title"])) < casefold(pyStr(discs[j]["title"]))
	})
	// The discs' genres are already tags (see genreTags). genre_aliases lets a client name its own
	// English genres (the editor's suggestions, or what was typed) the same way.
	aliases := genreAliases()
	genreSet := map[string]bool{}
	genres := []string{}
	for _, d := range discs {
		list, _ := d["genres"].([]any)
		for _, g := range list {
			s := pyStr(g)
			if !genreSet[s] {
				genreSet[s] = true
				genres = append(genres, s)
			}
		}
	}
	sort.SliceStable(genres, func(i, j int) bool { return casefold(genres[i]) < casefold(genres[j]) })
	mediaDir := cfg.DVDDir
	if cfg.DVDDir != cfg.CDDir {
		mediaDir = cfg.DVDDir + " and " + cfg.CDDir
	}
	var playing any
	for _, d := range cat.values() {
		if d.State == playingPath {
			playing = d.ID
			break
		}
	}
	writeJSON(w, 200, map[string]any{"discs": discs, "genres": genres, "genre_aliases": aliases, "media_dir": mediaDir,
		"tmdb": cfg.TMDBKey != "", "playing": playing})
}

func handleCover(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	if c := findCustomCover(d.FP); c != "" {
		serveFile(w, r, c, 3600, "")
		return
	}
	if d.UserCover != "" {
		serveFile(w, r, d.UserCover, 3600, "")
		return
	}
	if cached := filepath.Join(cfg.CoverDir, d.ID+".jpg"); isFile(cached) {
		serveFile(w, r, cached, 86400, "image/jpeg")
		return
	}
	ov := libRec(loadLibrary(), d.FP)
	title := pyStr(firstTruthy(localizedTitle(d, readMeta(d.ID)), ov["title"]))
	var year any = d.Year
	if d.Year == "" {
		year = nil
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, placeholderSVG(title, firstTruthy(year, ov["year"]), d.Kind == "cd"))
}

func handleMetaPut(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	if payload == nil {
		jsonError(w, 400, "Invalid JSON")
		return
	}
	clean, err := cleanMeta(payload)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	libMu.Lock()
	lib := loadLibrary()
	if len(clean) > 0 {
		lib[d.FP] = clean
	} else {
		delete(lib, d.FP)
	}
	err = saveLibrary(lib)
	libMu.Unlock()
	if err != nil {
		panic(err)
	}
	if cfg.TMDBKey != "" && d.Kind != "cd" {
		go enrichAll() // a new season fetches its poster
	}
	writeJSON(w, 200, discView(d, lib))
}

func handleWatched(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	watched := payload != nil && truthy(payload["watched"])
	libMu.Lock()
	lib := loadLibrary()
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
	err := saveLibrary(lib)
	libMu.Unlock()
	if err != nil {
		panic(err)
	}
	writeJSON(w, 200, map[string]any{"watched": watched})
}

func handleLookup(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	query := ""
	if payload != nil {
		query = runeSlice(pyStrip(pyStr(getOr(payload, "query", ""))), 200)
	}
	isCD := d.Kind == "cd"
	if !isCD && cfg.TMDBKey == "" {
		jsonError(w, 400, "Set TMDB_API_KEY in the .env file to search for DVDs")
		return
	}
	if !acquireEnrich(60 * time.Second) {
		jsonError(w, 503, "A background lookup is still running, try again in a minute")
		return
	}
	found, err := lookup(d, query)
	releaseEnrich()
	if err != nil {
		jsonError(w, 502, "Lookup failed: "+errText(err))
		return
	}
	cover := true
	if found {
		d.mu.Lock()
		if d.coverFound != nil {
			cover = *d.coverFound
			d.coverFound = nil
		}
		d.mu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"found": found, "cover": cover, "disc": discView(d, loadLibrary())})
}

func handleResumeClear(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	setResume(d.FP, nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}

var thumbRE = regexp.MustCompile(`/w\d+$`)

func handlePosters(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil || tmdbDVDError(w, d) {
		return
	}
	thumbURL := thumbRE.ReplaceAllString(cfg.TMDBImg, "/w185") // TMDB_IMAGE_URL's "w500" becomes "w185"
	meta := readMeta(d.ID)
	terms := searchTerms(d, "", nil)
	q := r.URL.Query()
	kind, tmdbID := q.Get("tmdb_kind"), q.Get("tmdb_id")
	if kind != "" || tmdbID != "" { // only the posters of one title: ?tmdb_kind=movie&tmdb_id=597
		if (kind != "tv" && kind != "movie") || !isAllDigits(tmdbID) {
			jsonError(w, 400, "Invalid title")
			return
		}
		id, _ := strconv.ParseInt(tmdbID, 10, 64)
		var season any
		if kind == "tv" {
			season = terms.DiscSeason
		}
		posters, err := tmdbPosters(kind, id, season)
		if err != nil {
			jsonError(w, 502, "Search failed: "+errText(err))
			return
		}
		writeJSON(w, 200, map[string]any{"posters": posters, "thumb_url": thumbURL})
		return
	}
	query := runeSlice(pyStrip(q.Get("query")), 200)
	terms = searchTerms(d, query, nil)
	matches, err := tmdbCandidates(terms)
	if err != nil {
		jsonError(w, 502, "Search failed: "+errText(err))
		return
	}
	var current any
	mk, _ := meta["tmdb_kind"].(string)
	if (mk == "tv" || mk == "movie") && truthy(meta["tmdb_id"]) {
		var season any
		if mk == "tv" {
			season = getOr(meta, "season", terms.DiscSeason)
		}
		posters, err := tmdbPosters(mk, meta["tmdb_id"], season)
		if err != nil {
			jsonError(w, 502, "Search failed: "+errText(err))
			return
		}
		current = map[string]any{
			"tmdb_kind":   mk,
			"tmdb_id":     meta["tmdb_id"],
			"title":       getOr(meta, "tmdb_title", ""),
			"year":        getOr(meta, "tmdb_year", ""),
			"season":      season,
			"poster_path": meta["poster_path"],
			"posters":     posters,
		}
	}
	writeJSON(w, 200, map[string]any{
		"query":     terms.Title,
		"current":   current,
		"matches":   matches,
		"thumb_url": thumbURL, // smaller images for the thumbnails
		"covered":   findCustomCover(d.FP) != "" || d.UserCover != "",
	})
}

func handlePosterSet(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil || tmdbDVDError(w, d) {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	pathV, pathOK := payload["poster_path"].(string)
	kind, _ := payload["tmdb_kind"].(string)
	tmdbID := payload["tmdb_id"]
	idNum, idOK := tmdbID.(interface{ Float64() (float64, error) })
	idValid := idOK && isPyInt(tmdbID)
	if idValid {
		f, _ := idNum.Float64()
		idValid = f > 0
	}
	if !pathOK || !posterPathRE.MatchString(pathV) || (kind != "tv" && kind != "movie") || !idValid {
		jsonError(w, 400, "Invalid poster")
		return
	}
	if !acquireEnrich(60 * time.Second) {
		jsonError(w, 503, "A background lookup is still running, try again in a minute")
		return
	}
	meta, terms, failed := func() (map[string]any, searchTermsT, bool) {
		defer releaseEnrich()
		meta := readMeta(d.ID)
		terms := searchTerms(d, "", nil)
		if !pyEqual(meta["tmdb_kind"], kind) || !pyEqual(meta["tmdb_id"], tmdbID) {
			details, err := tmdbDetails(kind, tmdbID)
			if err != nil {
				jsonError(w, 502, "Could not get the poster: "+errText(err))
				return nil, terms, true
			}
			meta = details
			meta["season"] = nil
			if kind == "tv" {
				meta["season"] = terms.DiscSeason
			}
			meta["matched"] = "(chosen by hand)"
			meta["whole"] = true
		}
		got, err := downloadPoster(d, pathV)
		if err != nil {
			jsonError(w, 502, "Could not get the poster: "+errText(err))
			return nil, terms, true
		}
		if !got {
			jsonError(w, 502, "TMDB could not send that poster")
			return nil, terms, true
		}
		return meta, terms, false
	}()
	if failed {
		return
	}
	delete(meta, "miss")
	meta["poster_path"] = pathV
	meta["poster_chosen"] = true
	meta["season_checked"] = terms.DiscSeason
	meta["searched"] = terms.AutoTitle
	meta["checked"] = now()
	if err := storeMeta(d.ID, meta); err != nil {
		panic(err)
	}
	writeJSON(w, 200, map[string]any{
		"disc":    discView(d, loadLibrary()),
		"covered": findCustomCover(d.FP) != "" || d.UserCover != "",
	})
}

func handleCoverUpload(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxContentLength)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			htmlError(w, 413)
			return
		}
	}
	f, _, err := r.FormFile("cover")
	if err != nil {
		jsonError(w, 400, "Missing cover file")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCoverBytes+1))
	if err != nil {
		panic(err)
	}
	if len(data) > maxCoverBytes {
		jsonError(w, 413, "Image too large (max 8 MB)")
		return
	}
	ext := sniffImage(data)
	if ext == "" {
		jsonError(w, 400, "Unsupported format (use JPG, PNG or WebP)")
		return
	}
	if err := os.MkdirAll(cfg.CustomCoverDir, 0o755); err != nil {
		panic(err)
	}
	for _, e := range customExts {
		_ = os.Remove(filepath.Join(cfg.CustomCoverDir, d.FP+e))
	}
	if err := os.WriteFile(filepath.Join(cfg.CustomCoverDir, d.FP+ext), data, 0o644); err != nil {
		panic(err)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleCoverDelete(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	for _, e := range customExts {
		_ = os.Remove(filepath.Join(cfg.CustomCoverDir, d.FP+e))
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handlePlay(w http.ResponseWriter, r *http.Request) {
	d := getDisc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	wantResume := payload != nil && truthy(payload["resume"])
	entry := resumeEntry(d.FP)
	var res *resumeAt
	if wantResume && len(entry) > 0 {
		t := pyInt(getOr(entry, "time", int64(0))) - 5
		if t < 0 {
			t = 0
		}
		res = &resumeAt{Title: pyInt(firstTruthy(int64(0), entry["title"])), Time: t}
	} else if len(entry) > 0 {
		setResume(d.FP, nil) // started over on purpose
	}
	pendMu.Lock()
	pending.set, pending.State, pending.Wait, pending.Resume = true, d.State, true, res
	pendMu.Unlock()
	if err := writeState(d.State); err != nil {
		panic(err)
	}
	writeJSON(w, 200, map[string]any{"player_url": "/player", "title": shownTitle(d), "kind": d.Kind})
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	if err := writeState(""); err != nil {
		panic(err)
	}
	go hls.stop()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handlePlayer(w http.ResponseWriter, r *http.Request) {
	d := playingDisc()
	out := map[string]any{"url": nekoURL(r), "title": nil, "id": nil, "kind": "dvd"}
	if d != nil {
		out["title"] = shownTitle(d)
		out["id"] = d.ID
		out["kind"] = d.Kind
		_, res, _ := pendingFor(d)
		out["resume"] = res != nil
		out["resume_at"] = nil
		if res != nil {
			out["resume_at"] = res.Time
		}
		if d.Kind == "cd" {
			out["artist"] = d.Artist
			out["tracks"] = cdTracksView(d)
			out["cover"] = fmt.Sprintf("/api/cover/%s?v=%d", d.ID, coverVersion(d))
		}
	}
	writeJSON(w, 200, out)
}

func handlePlayerStatus(w http.ResponseWriter, r *http.Request) {
	if readState() == "" {
		writeJSON(w, 200, map[string]any{"state": "idle"})
		return
	}
	d := playingDisc()
	var st map[string]any
	var err error
	if d != nil && d.Kind == "cd" {
		st, err = cdStatus(d)
		if err == nil && (st["state"] == "playing" || st["state"] == "paused") {
			var dur any
			if d.Duration != nil && *d.Duration != 0 {
				dur = int64(*d.Duration)
			}
			rememberPosition(d, st["abs"], dur, nil)
		}
	} else {
		st, err = vlc.status()
		if err == nil && d != nil && (st["state"] == "playing" || st["state"] == "paused") {
			rememberPosition(d, st["time"], st["length"], st["title"])
		}
	}
	if err != nil {
		var ve *VlcError
		if errors.As(err, &ve) { // VLC is still starting (or restarting for a new disc)
			writeJSON(w, 200, map[string]any{"state": "offline"})
			return
		}
		panic(err)
	}
	writeJSON(w, 200, st)
}

func handlePlayerCmd(w http.ResponseWriter, r *http.Request) {
	if readState() == "" {
		jsonError(w, 409, "Nothing is playing")
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	if payload == nil {
		jsonError(w, 400, "Invalid JSON")
		return
	}
	action := payload["action"]
	d := playingDisc()
	err := func() error {
		switch action {
		case "resume_apply":
			if d != nil {
				return applyResume(d)
			}
			return nil
		case "start":
			return startPlayback(d)
		case "nav":
			return vlc.nav(pyStr(getOr(payload, "value", "")))
		}
		if d != nil && d.Kind == "cd" {
			if handled, err := cdCommand(d, action, payload); handled || err != nil {
				return err
			}
		}
		number := func(lo, hi int64, set func(int64) error) error {
			v, err := intField(payload, "value", lo, hi)
			if err != nil {
				return err
			}
			return set(v)
		}
		switch action {
		case "toggle":
			return vlc.pause()
		case "seek":
			return number(0, 86400, vlc.seek)
		case "chapter_next":
			return vlc.nextChapter()
		case "chapter_prev":
			return vlc.prevChapter()
		case "volume":
			return number(0, 100, vlc.setVolume)
		case "audio":
			return number(0, 1000000, vlc.setAudio)
		case "subtitle":
			return number(-1, 1000000, vlc.setSubtitle)
		case "menu":
			return vlc.discMenu()
		case "return":
			return vlc.partMenu()
		}
		return errUnknownAction
	}()
	var ve *ValueError
	var vlcErr *VlcError
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]any{"ok": true})
	case err == errUnknownAction:
		jsonError(w, 400, "Unknown action")
	case errors.As(err, &ve):
		jsonError(w, 400, ve.msg)
	case errors.As(err, &vlcErr):
		jsonError(w, 503, "Player not ready")
	default:
		panic(err)
	}
}

var errUnknownAction = errors.New("unknown action")

func handleRescan(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"count": len(getCatalog(true).order)})
}

// ---------------------------------------------------------------------------------- routing
func routes() http.Handler {
	mux := http.NewServeMux()
	page := func(name string, maxAge int, ctype string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { serveFile(w, r, staticFile(name), maxAge, ctype) }
	}
	mux.HandleFunc("GET /{$}", page("index.html", -1, ""))
	mux.HandleFunc("GET /favicon.ico", page("icons/favicon.ico", 86400, ""))
	mux.HandleFunc("GET /manifest.webmanifest", page("manifest.webmanifest", -1, "application/manifest+json"))
	mux.HandleFunc("GET /player", page("player.html", -1, ""))
	mux.HandleFunc("GET /static/{path...}", func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.PathValue("path"))
		serveFile(w, r, staticFile(strings.TrimPrefix(p, "/")), -1, "")
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": version})
	})
	mux.HandleFunc("GET /api/discs", handleDiscs)
	mux.HandleFunc("GET /api/cover/{id}", handleCover)
	mux.HandleFunc("PUT /api/disc/{id}/meta", handleMetaPut)
	mux.HandleFunc("POST /api/disc/{id}/watched", handleWatched)
	mux.HandleFunc("POST /api/disc/{id}/lookup", handleLookup)
	mux.HandleFunc("POST /api/disc/{id}/resume/clear", handleResumeClear)
	mux.HandleFunc("GET /api/disc/{id}/posters", handlePosters)
	mux.HandleFunc("POST /api/disc/{id}/poster", handlePosterSet)
	mux.HandleFunc("POST /api/disc/{id}/cover", handleCoverUpload)
	mux.HandleFunc("DELETE /api/disc/{id}/cover", handleCoverDelete)
	mux.HandleFunc("POST /api/play/{id}", handlePlay)
	mux.HandleFunc("POST /api/stop", handleStop)
	mux.HandleFunc("POST /api/stream/hls", handleStreamHLS)
	mux.HandleFunc("GET /api/hls/{name}", handleHLSFile)
	mux.HandleFunc("GET /api/player", handlePlayer)
	mux.HandleFunc("GET /api/player/status", handlePlayerStatus)
	mux.HandleFunc("POST /api/player/cmd", handlePlayerCmd)
	mux.HandleFunc("POST /api/rescan", handleRescan)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("ERROR: %s %s: %v", r.Method, r.URL.Path, rec)
				htmlError(w, 500)
			}
		}()
		addCORS(w, r)
		if r.Method == http.MethodOptions { // Flask answers OPTIONS by itself; CORS preflights need it
			if allow := allowedMethods(mux, r); allow != "" {
				w.Header().Set("Allow", allow)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(200)
				return
			}
			htmlError(w, 404)
			return
		}
		if _, pattern := mux.Handler(r); pattern == "" { // Flask's pages, not Go's plain text
			if allow := allowedMethods(mux, r); allow != "" {
				w.Header().Set("Allow", allow)
				htmlError(w, 405)
				return
			}
			htmlError(w, 404)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// allowedMethods lists the methods a path answers to, as Flask's Allow header does.
func allowedMethods(mux *http.ServeMux, r *http.Request) string {
	var out []string
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" && strings.HasPrefix(pattern, m+" ") {
			out = append(out, m)
			if m == "GET" {
				out = append(out, "HEAD")
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return strings.Join(append(out, "OPTIONS"), ", ")
}

// addCORS lets other apps (a TV app) call the API from the origins in CORS_ORIGIN.
func addCORS(w http.ResponseWriter, r *http.Request) {
	if len(cfg.CORSOrigins) == 0 || !strings.HasPrefix(r.URL.Path, "/api/") {
		return
	}
	origin := r.Header.Get("Origin")
	allowed := false
	for _, o := range cfg.CORSOrigins {
		if o == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			allowed = true
			break
		}
	}
	if !allowed {
		for _, o := range cfg.CORSOrigins {
			if o == origin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				allowed = true
				break
			}
		}
	}
	if allowed {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
}
