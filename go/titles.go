package main

// Titles from file names, and the season and disc numbers written in them. Keep in step with
// seasonOf() and parseFilmDisc() in static/app.js, which group the discs in the catalog.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	yearParenRE = regexp.MustCompile(`\(((?:19|20)\d{2})\)`)
	yearOrTagRE = regexp.MustCompile(`\((?:19|20)\d{2}\)|\[[^\]]*\]`)
	underDotRE  = regexp.MustCompile(`[_.]+`)
	yearWordRE  = regexp.MustCompile(`^(?:19|20)\d{2}$`)
)

var genericTitles = map[string]bool{
	"videots": true, "dvd": true, "dvdvideo": true, "disc": true, "disco": true,
	"movie": true, "filme": true, "video": true,
}

// cleanTitle turns a file name or volume label into a readable title: (title, year or "").
func cleanTitle(raw string) (string, string) {
	year := ""
	if m := yearParenRE.FindStringSubmatch(raw); m != nil {
		year = m[1]
	}
	s := yearOrTagRE.ReplaceAllString(raw, " ")
	s = underDotRE.ReplaceAllString(s, " ")
	s = strings.Trim(collapseSpace(s), " -")
	if s != "" && strings.ToUpper(s) == s {
		s = pyTitle(s)
	}
	return s, year
}

func isGeneric(title string) bool {
	var b strings.Builder
	for _, r := range pyLower(title) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return title == "" || genericTitles[b.String()]
}

// words is re.sub(r"[^\w]+", " ", text.lower()).split().
func words(text string) []string {
	return strings.FieldsFunc(pyLower(text), func(r rune) bool { return !isWordRune(r) })
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func hasPrefixFold(r []rune, at int, word string) bool {
	w := []rune(word)
	if at+len(w) > len(r) {
		return false
	}
	for i, c := range w {
		if unicode.ToLower(r[at+i]) != c {
			return false
		}
	}
	return true
}

func countWhile(r []rune, at int, ok func(rune) bool) int {
	n := 0
	for at+n < len(r) && ok(r[at+n]) {
		n++
	}
	return n
}

// numberAt matches \s*0*(\d{1,max}) at r[at:] with the regex's backtracking (greedy zeros, then
// greedy digits) and returns the first number whose end passes the lookahead.
func numberAt(r []rune, at, max int, ahead func([]rune, int) bool) (int, bool) {
	q := at + countWhile(r, at, isPySpace)
	zeros := countWhile(r, q, func(c rune) bool { return c == '0' })
	for z := zeros; z >= 0; z-- {
		start := q + z
		avail := countWhile(r, start, isDigit)
		if avail > max {
			avail = max
		}
		for d := avail; d >= 1; d-- {
			if ahead(r, start+d) {
				n, _ := strconv.Atoi(string(r[start : start+d]))
				return n, true
			}
		}
	}
	return 0, false
}

func inSet(c rune, set string) bool {
	return strings.ContainsRune(set, c) || (strings.Contains(set, " ") && isPySpace(c))
}

// Season: (?:^|[\s\-–:,(])(?:season|temporada|saison|staffel|s|t)\s*0*(\d{1,3})
//
//	(?=$|[\s\-–:,)]|d(?:is[ck]o?)?\s*\d|e\d)
var seasonWords = []string{"season", "temporada", "saison", "staffel", "s", "t"}

func seasonAhead(r []rune, e int) bool {
	if e == len(r) || inSet(r[e], " -–:,)") {
		return true
	}
	c := unicode.ToLower(r[e])
	if c == 'e' {
		return e+1 < len(r) && isDigit(r[e+1])
	}
	if c != 'd' {
		return false
	}
	digitAfterSpace := func(at int) bool {
		at += countWhile(r, at, isPySpace)
		return at < len(r) && isDigit(r[at])
	}
	p := e + 1
	if hasPrefixFold(r, p, "is") && p+2 < len(r) && (unicode.ToLower(r[p+2]) == 'c' || unicode.ToLower(r[p+2]) == 'k') {
		if p+3 < len(r) && unicode.ToLower(r[p+3]) == 'o' && digitAfterSpace(p+4) {
			return true
		}
		if digitAfterSpace(p + 3) {
			return true
		}
	}
	return digitAfterSpace(p)
}

// Film disc: (?:^|[\s\-–:,(\[])(?:dis[ck]o?|dvd|cd|d)\s*0*(\d{1,2})
//
//	(?=$|[\s\-–:,)\]]|\s*(?:of|de|/)\s*\d)
func filmAhead(r []rune, e int) bool {
	if e == len(r) || inSet(r[e], " -–:,)]") {
		return true
	}
	at := e + countWhile(r, e, isPySpace)
	switch {
	case hasPrefixFold(r, at, "of"), hasPrefixFold(r, at, "de"):
		at += 2
	case at < len(r) && r[at] == '/':
		at++
	default:
		return false
	}
	at += countWhile(r, at, isPySpace)
	return at < len(r) && isDigit(r[at])
}

// filmWordLengths lists the lengths the keyword can take at r[at:], in the regex's order.
func filmWordLengths(r []rune, at int) []int {
	var out []int
	if hasPrefixFold(r, at, "dis") && at+3 < len(r) && (unicode.ToLower(r[at+3]) == 'c' || unicode.ToLower(r[at+3]) == 'k') {
		if at+4 < len(r) && unicode.ToLower(r[at+4]) == 'o' {
			out = append(out, 5)
		}
		out = append(out, 4)
	}
	if hasPrefixFold(r, at, "dvd") {
		out = append(out, 3)
	}
	if hasPrefixFold(r, at, "cd") {
		out = append(out, 2)
	}
	if hasPrefixFold(r, at, "d") {
		out = append(out, 1)
	}
	return out
}

// search finds the leftmost match: the start of the match (the separator before the keyword, or
// 0) and the number. wordLens gives the keyword lengths to try at a position.
func search(r []rune, open string, wordLens func([]rune, int) []int, max int,
	ahead func([]rune, int) bool) (int, int, bool) {
	for s := 0; s <= len(r); s++ {
		var starts []int
		if s == 0 {
			starts = append(starts, 0)
		}
		if s < len(r) && inSet(r[s], open) {
			starts = append(starts, s+1)
		}
		for _, k := range starts {
			for _, l := range wordLens(r, k) {
				if n, ok := numberAt(r, k+l, max, ahead); ok {
					return s, n, true
				}
			}
		}
	}
	return 0, 0, false
}

func seasonWordLengths(r []rune, at int) []int {
	var out []int
	for _, w := range seasonWords {
		if hasPrefixFold(r, at, w) {
			out = append(out, len([]rune(w)))
		}
	}
	return out
}

func nameBefore(r []rune, end int, trim string) string {
	name := r[:end]
	for len(name) > 0 && inSet(name[len(name)-1], trim) {
		name = name[:len(name)-1]
	}
	return pyStrip(string(name))
}

// parseSeason gives (series name, season) from a title that names a season. A name must come
// first, so a film like "T2 Trainspotting" is not taken for season 2.
func parseSeason(title string) (string, int, bool) {
	r := []rune(title)
	start, n, ok := search(r, " -–:,(", seasonWordLengths, 3, seasonAhead)
	if !ok {
		return "", 0, false
	}
	name := nameBefore(r, start, " -–:,(")
	return name, n, name != ""
}

// parseFilmDisc gives (film name, disc number) from a title like "Titanic Disc 2".
func parseFilmDisc(title string) (string, int, bool) {
	r := []rune(title)
	start, n, ok := search(r, " -–:,([", filmWordLengths, 2, filmAhead)
	if !ok {
		return "", 0, false
	}
	name := nameBefore(r, start, " -–:,([")
	return name, n, name != ""
}

// filmName is the title without a "Disc 2" in it: what to search for online.
func filmName(title string) string {
	if name, _, ok := parseFilmDisc(title); ok {
		return name
	}
	return title
}

// pyStem is Path(name).stem: the last component without its last suffix.
func pyStem(path string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[:i]
	}
	return name
}

// discSeason is the series name and season of a DVD (season nil when none is known). rec holds
// the user's edits; the series name is the title minus any "S02 D1" in it.
func discSeason(d *Disc, rec map[string]any) (string, any) {
	title := d.Title
	if truthy(rec["title"]) {
		title = pyStr(rec["title"])
	}
	fname, fseason, fromTitle := parseSeason(title)
	pseason, parsed := fseason, fromTitle
	if !parsed {
		_, pseason, parsed = parseSeason(underDotRE.ReplaceAllString(pyStem(d.Rel), " "))
	}
	if !parsed {
		_, pseason, parsed = parseSeason(underDotRE.ReplaceAllString(d.Label, " "))
	}
	name := title
	if fromTitle && fname != "" {
		name = fname
	}
	var season any
	if v, ok := rec["season"]; ok && v != nil {
		season = v
	} else if parsed {
		season = pseason
	}
	return name, season
}

// titleVariants: the search queries to try for a title, most specific first. Long file names
// often find nothing as a whole, but the series name alone does. Shorter queries are strict: a
// result must contain all their words.
type variant struct {
	query  string
	strict bool
}

func titleVariants(title string) []variant {
	var out []variant
	add := func(q string, strict bool) {
		q = pyStrip(collapseSpace(q))
		if q == "" {
			return
		}
		for _, v := range out {
			if pyLower(v.query) == pyLower(q) {
				return
			}
		}
		out = append(out, variant{q, strict})
	}
	add(title, false)
	plain := strings.Join(words(title), " ")
	add(plain, false)
	for _, sep := range []string{":", " - ", " – ", " — "} {
		if i := strings.Index(title, sep); i >= 0 {
			add(title[:i], true)
			add(title[i+len(sep):], true)
		}
	}
	ws := pySplit(plain)
	for n := len(ws) - 1; n > 1; n-- {
		add(strings.Join(ws[:n], " "), true)
	}
	if len(out) > 7 {
		out = out[:7]
	}
	return out
}

func wordSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, w := range words(text) {
		set[w] = true
	}
	return set
}

func subset(a, b map[string]bool) bool {
	for w := range a {
		if !b[w] {
			return false
		}
	}
	return true
}

// isPartOf tells whether query is only a part of name, as the shorter searches of titleVariants
// are. A year in the name does not count.
func isPartOf(query, name string) bool {
	noYears := func(t string) map[string]bool {
		set := map[string]bool{}
		for _, w := range words(t) {
			if !yearWordRE.MatchString(w) {
				set[w] = true
			}
		}
		return set
	}
	a, b := noYears(query), noYears(name)
	return len(a) < len(b) && subset(a, b)
}

// localizedTitle puts the name of the disc's TMDB match, in TMDB_LANGUAGE, in place of the name
// in the file, keeping any "S01 D2" or "Disc 2" after it. A match found by only a part of a long
// name keeps the file's.
func localizedTitle(d *Disc, meta map[string]any) string {
	title := d.Title
	tmdbTitle, _ := meta["tmdb_title"].(string)
	if !truthy(meta["tmdb_title"]) || d.Kind == "cd" {
		return title
	}
	if tmdbTitle == "" {
		tmdbTitle = pyStr(meta["tmdb_title"])
	}
	name := title
	if n, _, ok := parseSeason(title); ok {
		name = n
	} else if n, _, ok := parseFilmDisc(title); ok {
		name = n
	}
	var whole bool
	if w, ok := meta["whole"]; ok && w != nil {
		whole = truthy(w)
	} else {
		matched := ""
		if truthy(meta["matched"]) {
			matched = pyStr(meta["matched"])
		}
		whole = !isPartOf(matched, name)
	}
	if !whole {
		return title
	}
	return tmdbTitle + string([]rune(title)[len([]rune(name)):])
}
