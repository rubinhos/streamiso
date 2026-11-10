// Package musicbrainz finds albums on MusicBrainz and their covers on the Cover Art Archive (both free,
// no key). It implements ports.MusicDB. MusicBrainz asks clients to identify themselves and to stay under
// one request per second.
// https://musicbrainz.org/doc/MusicBrainz_API  ·  https://musicbrainz.org/doc/Cover_Art_Archive/API
package musicbrainz

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"streamiso/internal/adapters/httpx"
	"streamiso/internal/compat"
	"streamiso/internal/config"
	"streamiso/internal/domain"
	"streamiso/internal/ports"
	"strings"
	"time"
)

type Client struct {
	URL, CAAURL string
	Contact     string // put in the User-Agent, as MusicBrainz asks
}

// Pause waits as long as MusicBrainz asks between two requests.
func (c *Client) Pause() { time.Sleep(mbDelay) }

func (c *Client) userAgent() string {
	if c.Contact != "" {
		return "StreamISO/" + config.Version + " ( " + c.Contact + " )"
	}
	return "StreamISO/" + config.Version
}

func (c *Client) get(rawURL string, timeout time.Duration, limit int64) ([]byte, error) {
	return httpx.Get(rawURL, map[string]string{"User-Agent": c.userAgent(), "Accept": userAgentAccpt}, timeout, limit)
}

var nonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

// Norm is the text lowered, with anything but letters and digits as single spaces.
func Norm(text string) string {
	return compat.Strip(nonAlnumRE.ReplaceAllString(compat.Lower(text), " "))
}

// lucene quotes a value for a Lucene phrase query.
func lucene(text string) string {
	return `"` + compat.Strip(strings.NewReplacer(`\`, " ", `"`, " ").Replace(text)) + `"`
}

// Variants: the titles to try, most specific first. Shorter ones are matched by domain.Words.
func Variants(title string) []domain.Variant {
	var out []domain.Variant
	add := func(q string, strict bool) {
		q = compat.Strip(compat.CollapseSpace(q))
		if q == "" {
			return
		}
		for _, v := range out {
			if Norm(v.Query) == Norm(q) {
				return
			}
		}
		out = append(out, domain.Variant{Query: q, Strict: strict})
	}
	add(title, false)
	for _, sep := range []string{":", " - ", " – ", " — "} {
		if i := strings.Index(title, sep); i >= 0 {
			add(title[:i], true)
			add(title[i+len(sep):], true)
		}
	}
	ws := compat.Split(Norm(title))
	for n := len(ws) - 1; n > 2; n-- { // keep at least three domain.Words
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

func (c *Client) search(title, artist string, strict bool) (*ports.AlbumHit, error) {
	query := "releasegroup:" + lucene(title)
	if artist != "" {
		query += " AND artist:" + lucene(artist)
	}
	u := c.URL + "/release-group/?" + "query=" + url.QueryEscape(query) + "&fmt=json&limit=8"
	body, err := c.get(u, 15*time.Second, 0)
	if err != nil {
		return nil, err
	}
	v, err := compat.DecodeJSON(body)
	if err != nil {
		return nil, err
	}
	data, _ := v.(map[string]any)
	wantTitle, wantArtist := Norm(title), Norm(artist)
	wantWords := setOf(compat.Split(wantTitle))
	groups, _ := data["release-groups"].([]any)
	for _, g := range groups {
		rg, ok := g.(map[string]any)
		if !ok {
			continue
		}
		score := 0.0
		if s, ok := rg["score"]; ok {
			f, ok := compat.ToFloat(s)
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
		got := Norm(compat.StrOr(rg["title"], ""))
		gotWords := setOf(compat.Split(got))
		if strict {
			// a shortened query must be fully contained in the album title (a near match is more
			// often another album than the right one)
			if !domain.Subset(wantWords, gotWords) {
				continue
			}
		} else if got != wantTitle && !(domain.Subset(wantWords, gotWords) || domain.Subset(gotWords, wantWords)) {
			continue
		}
		var names []string
		credits, _ := rg["artist-credit"].([]any)
		for _, c := range credits {
			if cm, ok := c.(map[string]any); ok {
				names = append(names, compat.StrOr(cm["name"], ""))
			}
		}
		credit := strings.Join(names, " ")
		if wantArtist != "" && !strings.Contains(Norm(credit), wantArtist) && !strings.Contains(wantArtist, Norm(credit)) {
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
				c, _ := compat.ToFloat(tm["count"])
				ts = append(ts, tag{tm["name"], c})
			}
		}
		sort.SliceStable(ts, func(i, j int) bool { return ts[i].count > ts[j].count })
		genres := []string{}
		for i, t := range ts {
			if i >= 3 {
				break
			}
			if compat.Truthy(t.name) {
				genres = append(genres, compat.Title(compat.Str(t.name)))
			}
		}
		year := compat.Slice(compat.StrOr(rg["first-release-date"], ""), 4)
		if !compat.IsAllDigits(year) {
			year = ""
		}
		id, ok := rg["id"]
		if !ok {
			return nil, fmt.Errorf("'id'")
		}
		return &ports.AlbumHit{MBID: compat.Str(id), Title: compat.StrOr(rg["title"], ""), Artist: credit, Year: year, Genres: genres}, nil
	}
	return nil, nil
}

// LookupAlbum finds the best matching release group: the full title with the artist, then the
// full title alone (cue sheets often carry a wrong or generic artist), then shorter titles.
func (c *Client) LookupAlbum(title, artist string) (*ports.AlbumHit, error) {
	if title == "" {
		return nil, nil
	}
	first := true
	for _, v := range Variants(title) {
		artists := []string{""}
		if artist != "" && !v.Strict {
			artists = []string{artist, ""}
		}
		for _, a := range artists {
			if !first {
				time.Sleep(mbDelay) // MusicBrainz: at most one request per second
			}
			first = false
			hit, err := c.search(v.Query, a, v.Strict)
			if err != nil || hit != nil {
				return hit, err
			}
		}
	}
	return nil, nil
}

func (c *Client) caaImage(path string) ([]byte, error) {
	data, err := c.get(c.CAAURL+"/"+path, 25*time.Second, MaxCoverBytes)
	if err != nil {
		var h *ports.HTTPError
		if errors.As(err, &h) && (h.Code == 404 || h.Code == 400) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) > MaxCoverBytes {
		return nil, nil
	}
	if (len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff") || (len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n") {
		return data, nil
	}
	return nil, nil
}

// FetchCover gets the front cover (500 px) of a release group. The archive often has art only
// for individual releases, so when the group has none, the releases in it are tried one by one.
func (c *Client) FetchCover(mbid string) ([]byte, error) {
	data, err := c.caaImage("release-group/" + mbid + "/front-500")
	if err != nil || data != nil {
		return data, err
	}
	time.Sleep(mbDelay)
	u := c.URL + "/release?" + "release-group=" + url.QueryEscape(mbid) + "&fmt=json&limit=25"
	body, err := c.get(u, 15*time.Second, 0)
	if err != nil {
		return nil, nil
	}
	v, err := compat.DecodeJSON(body)
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
		if len(art) > 0 && !(compat.Truthy(art["front"]) || compat.Truthy(art["artwork"])) {
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
		data, err := c.caaImage("release/" + compat.Str(id) + "/front-500")
		if err != nil || data != nil {
			return data, err
		}
	}
	return nil, nil
}

const (
	mbMinScore = 85
	// MaxCoverBytes is the largest cover accepted, from the archive or uploaded.
	MaxCoverBytes  = 8 * 1024 * 1024
	mbDelay        = 1100 * time.Millisecond // MusicBrainz allows about one request per second
	userAgentAccpt = "*/*"
)
