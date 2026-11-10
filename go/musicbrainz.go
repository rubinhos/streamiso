package main

// Album lookup on MusicBrainz and cover art from the Cover Art Archive (both free, no key).
// MusicBrainz asks clients to identify themselves and to stay under one request per second.
// https://musicbrainz.org/doc/MusicBrainz_API  ·  https://musicbrainz.org/doc/Cover_Art_Archive/API

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	mbMinScore     = 85
	maxCoverBytes  = 8 * 1024 * 1024
	mbDelay        = 1100 * time.Millisecond // MusicBrainz allows about one request per second
	userAgentAccpt = "*/*"
)

// HTTPError is an HTTP answer with an error status (urllib's HTTPError).
type HTTPError struct {
	Code int
	URL  string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP Error %d", e.Code) }

func isHTTPError(err error) (*HTTPError, bool) {
	var h *HTTPError
	ok := errors.As(err, &h)
	return h, ok
}

func mbUserAgent() string {
	if cfg.MBContact != "" {
		return "StreamISO/" + version + " ( " + cfg.MBContact + " )"
	}
	return "StreamISO/" + version
}

var httpClient = &http.Client{}

// httpGet fetches a URL (following redirects). limit > 0 reads at most limit+1 bytes.
func httpGet(rawURL string, headers map[string]string, timeout time.Duration, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c := *httpClient
	c.Timeout = timeout
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return nil, &HTTPError{Code: resp.StatusCode, URL: rawURL}
	}
	var r io.Reader = resp.Body
	if limit > 0 {
		r = io.LimitReader(resp.Body, limit+1)
	}
	return io.ReadAll(r)
}

func mbGet(rawURL string, timeout time.Duration, limit int64) ([]byte, error) {
	return httpGet(rawURL, map[string]string{"User-Agent": mbUserAgent(), "Accept": userAgentAccpt}, timeout, limit)
}

var nonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

func mbNorm(text string) string {
	return pyStrip(nonAlnumRE.ReplaceAllString(pyLower(text), " "))
}

// lucene quotes a value for a Lucene phrase query.
func lucene(text string) string {
	return `"` + pyStrip(strings.NewReplacer(`\`, " ", `"`, " ").Replace(text)) + `"`
}

// mbVariants: the titles to try, most specific first. Shorter ones are matched by words.
func mbVariants(title string) []variant {
	var out []variant
	add := func(q string, strict bool) {
		q = pyStrip(collapseSpace(q))
		if q == "" {
			return
		}
		for _, v := range out {
			if mbNorm(v.query) == mbNorm(q) {
				return
			}
		}
		out = append(out, variant{q, strict})
	}
	add(title, false)
	for _, sep := range []string{":", " - ", " – ", " — "} {
		if i := strings.Index(title, sep); i >= 0 {
			add(title[:i], true)
			add(title[i+len(sep):], true)
		}
	}
	ws := pySplit(mbNorm(title))
	for n := len(ws) - 1; n > 2; n-- { // keep at least three words
		add(strings.Join(ws[:n], " "), true)
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func setOf(ws []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range ws {
		m[w] = true
	}
	return m
}

type albumHit struct {
	MBID, Title, Artist, Year string
	Genres                    []string
}

func mbSearch(title, artist string, strict bool) (*albumHit, error) {
	query := "releasegroup:" + lucene(title)
	if artist != "" {
		query += " AND artist:" + lucene(artist)
	}
	u := cfg.MBURL + "/release-group/?" + "query=" + url.QueryEscape(query) + "&fmt=json&limit=8"
	body, err := mbGet(u, 15*time.Second, 0)
	if err != nil {
		return nil, err
	}
	v, err := decodeJSON(body)
	if err != nil {
		return nil, err
	}
	data, _ := v.(map[string]any)
	wantTitle, wantArtist := mbNorm(title), mbNorm(artist)
	wantWords := setOf(pySplit(wantTitle))
	groups, _ := data["release-groups"].([]any)
	for _, g := range groups {
		rg, ok := g.(map[string]any)
		if !ok {
			continue
		}
		score := 0.0
		if s, ok := rg["score"]; ok {
			f, ok := toFloat(s)
			if !ok {
				if str, isStr := s.(string); isStr {
					fmt.Sscan(str, &f)
				}
			}
			score = f
		}
		if int(score) < mbMinScore {
			continue
		}
		got := mbNorm(pyStrOr(rg["title"], ""))
		gotWords := setOf(pySplit(got))
		if strict {
			// a shortened query must be fully contained in the album title (a near match is more
			// often another album than the right one)
			if !subset(wantWords, gotWords) {
				continue
			}
		} else if got != wantTitle && !(subset(wantWords, gotWords) || subset(gotWords, wantWords)) {
			continue
		}
		var names []string
		credits, _ := rg["artist-credit"].([]any)
		for _, c := range credits {
			if cm, ok := c.(map[string]any); ok {
				names = append(names, pyStrOr(cm["name"], ""))
			}
		}
		credit := strings.Join(names, " ")
		if wantArtist != "" && !strings.Contains(mbNorm(credit), wantArtist) && !strings.Contains(wantArtist, mbNorm(credit)) {
			continue
		}
		tags, _ := rg["tags"].([]any)
		type tag struct {
			name  any
			count float64
		}
		var ts []tag
		for _, t := range tags {
			if tm, ok := t.(map[string]any); ok {
				c, _ := toFloat(tm["count"])
				ts = append(ts, tag{tm["name"], c})
			}
		}
		sort.SliceStable(ts, func(i, j int) bool { return ts[i].count > ts[j].count })
		genres := []string{}
		for i, t := range ts {
			if i >= 3 {
				break
			}
			if truthy(t.name) {
				genres = append(genres, pyTitle(pyStr(t.name)))
			}
		}
		year := runeSlice(pyStrOr(rg["first-release-date"], ""), 4)
		if !isAllDigits(year) {
			year = ""
		}
		id, ok := rg["id"]
		if !ok {
			return nil, fmt.Errorf("'id'")
		}
		return &albumHit{MBID: pyStr(id), Title: pyStrOr(rg["title"], ""), Artist: credit, Year: year, Genres: genres}, nil
	}
	return nil, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pyStrOr is (value or default) for a value used as text.
func pyStrOr(v any, def string) string {
	if !truthy(v) {
		return def
	}
	return pyStr(v)
}

// lookupAlbum finds the best matching release group: the full title with the artist, then the
// full title alone (cue sheets often carry a wrong or generic artist), then shorter titles.
func lookupAlbum(title, artist string) (*albumHit, error) {
	if title == "" {
		return nil, nil
	}
	first := true
	for _, v := range mbVariants(title) {
		artists := []string{""}
		if artist != "" && !v.strict {
			artists = []string{artist, ""}
		}
		for _, a := range artists {
			if !first {
				time.Sleep(mbDelay) // MusicBrainz: at most one request per second
			}
			first = false
			hit, err := mbSearch(v.query, a, v.strict)
			if err != nil || hit != nil {
				return hit, err
			}
		}
	}
	return nil, nil
}

func caaImage(path string) ([]byte, error) {
	data, err := mbGet(cfg.CAAURL+"/"+path, 25*time.Second, maxCoverBytes)
	if err != nil {
		if h, ok := isHTTPError(err); ok && (h.Code == 404 || h.Code == 400) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) > maxCoverBytes {
		return nil, nil
	}
	if (len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff") || (len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n") {
		return data, nil
	}
	return nil, nil
}

// fetchCover gets the front cover (500 px) of a release group. The archive often has art only
// for individual releases, so when the group has none, the releases in it are tried one by one.
func fetchCover(mbid string) ([]byte, error) {
	data, err := caaImage("release-group/" + mbid + "/front-500")
	if err != nil || data != nil {
		return data, err
	}
	time.Sleep(mbDelay)
	u := cfg.MBURL + "/release?" + "release-group=" + url.QueryEscape(mbid) + "&fmt=json&limit=25"
	body, err := mbGet(u, 15*time.Second, 0)
	if err != nil {
		return nil, nil
	}
	v, err := decodeJSON(body)
	if err != nil {
		return nil, nil
	}
	m, _ := v.(map[string]any)
	releases, _ := m["releases"].([]any)
	tried := 0
	for _, r := range releases {
		rel, ok := r.(map[string]any)
		if !ok {
			continue
		}
		art, _ := rel["cover-art-archive"].(map[string]any)
		if len(art) > 0 && !(truthy(art["front"]) || truthy(art["artwork"])) {
			continue // the archive says this release has no front cover
		}
		if tried >= 6 {
			break
		}
		tried++
		id, ok := rel["id"]
		if !ok {
			return nil, fmt.Errorf("'id'")
		}
		data, err := caaImage("release/" + pyStr(id) + "/front-500")
		if err != nil || data != nil {
			return data, err
		}
	}
	return nil, nil
}
