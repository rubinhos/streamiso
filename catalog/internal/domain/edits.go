// The user's edits of a disc (title, year, genres, synopsis, season and disc, watched), as checked
// before they are saved in library.json.

package domain

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"streamiso/internal/compat"
	"strings"
)

// ValueError is a bad value in a command (answered with 400).
type ValueError struct{ Msg string }

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
		s := strings.ReplaceAll(compat.Strip(x), "_", "")
		if !intTextRE.MatchString(s) || strings.Contains(compat.Strip(x), "__") {
			return nil, bad
		}
		p, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, bad
		}
		n = p
	default:
		f, ok := compat.ToFloat(v)
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

// CleanMeta validates and normalizes the edits; empty fields are dropped (they fall back to the
// automatic value).
func CleanMeta(p map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if t := compat.Strip(compat.StrOr(p["title"], "")); t != "" {
		out["title"] = compat.Slice(t, 200)
	}
	if t := compat.Strip(compat.StrOr(p["sort_title"], "")); t != "" { // sorts the disc under another name
		out["sort_title"] = compat.Slice(t, 200)
	}
	year, err := intOrNone(p["year"], 1888, 2100, "Year")
	if err != nil {
		return nil, err
	}
	if year != nil {
		out["year"] = year
	}
	var list []any
	switch g := compat.FirstTruthy([]any{}, p["genres"]).(type) {
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
		s := compat.Slice(compat.Strip(compat.Str(g)), 40)
		if s != "" && !seen[compat.Casefold(s)] {
			seen[compat.Casefold(s)] = true
			clean = append(clean, s)
		}
	}
	if len(clean) > 0 {
		if len(clean) > 12 {
			clean = clean[:12]
		}
		out["genres"] = clean
	}
	if s := compat.Strip(compat.StrOr(p["synopsis"], "")); s != "" {
		out["synopsis"] = compat.Slice(s, 4000)
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
	if compat.Truthy(p["watched"]) {
		out["watched"] = true
	}
	// Covers fill their space (cropped) unless "cover_fill": false shows this one whole. Only that
	// is stored; a payload without the field keeps the default.
	if b, ok := p["cover_fill"].(bool); ok && !b {
		out["cover_fill"] = false
	}
	return out, nil
}

func (e *ValueError) Error() string { return e.Msg }

// MaxCoverBytes is the largest cover image accepted.
const MaxCoverBytes = 8 * 1024 * 1024

// SniffImage is the file extension of a JPEG, PNG or WebP image, or "" for anything else.
func SniffImage(data []byte) string {
	switch {
	case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
		return ".jpg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	}
	return ""
}
