package main

// Audio CD images: reads .cue sheets (BIN/CUE).
//
// Only the common "one image file" layout is supported: a single FILE entry that holds every
// track, all of them AUDIO. Raw BINARY images are 44.1 kHz, 16-bit, stereo, 2352-byte sectors,
// 75 sectors per second. VLC plays them directly as one continuous stream, and a track is just a
// position in it (which also gives gapless playback, like a real CD).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	sectorsPerSecond = 75
	bytesPerSecond   = 44100 * 2 * 2 // 44.1 kHz * 16 bit * stereo = 176400
)

var audioFileTypes = map[string]bool{"WAVE": true, "MP3": true, "AIFF": true, "FLAC": true}

type CueError struct{ msg string }

func (e *CueError) Error() string { return e.msg }

func cueErr(format string, a ...any) error { return &CueError{fmt.Sprintf(format, a...)} }

type Track struct {
	N      int
	Title  string
	Artist string
	Start  float64
	hasIdx bool
}

type CueInfo struct {
	Text      string
	Album     string
	Artist    string
	Year      string
	Genre     string
	Bin       string
	ByteOrder string
	Raw       bool
	Tracks    []Track
	Duration  *float64
}

// Span is a track with its start, end and length in seconds (nil when the end is unknown).
type Span struct {
	N      int
	Title  string
	Artist string
	Start  float64
	End    *float64
	Length *float64
}

// cp1252 bytes 0x80-0x9F; 0 marks the five bytes Python's cp1252 codec cannot decode.
var cp1252High = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

// readCueText decodes the sheet as UTF-8 (without a BOM), else cp1252, else Latin-1.
func readCueText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw := make([]byte, 512*1024)
	n, err := readFull(f, raw)
	if err != nil {
		return "", err
	}
	raw = raw[:n]
	if utf8.Valid(raw) {
		return string(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))), nil
	}
	var b strings.Builder
	ok := true
	for _, c := range raw {
		switch {
		case c < 0x80 || c >= 0xA0:
			b.WriteRune(rune(c))
		case cp1252High[c-0x80] != 0:
			b.WriteRune(cp1252High[c-0x80])
		default:
			ok = false
		}
		if !ok {
			break
		}
	}
	if ok {
		return b.String(), nil
	}
	b.Reset()
	for _, c := range raw { // Latin-1 decodes every byte
		b.WriteRune(rune(c))
	}
	return b.String(), nil
}

func readFull(f *os.File, buf []byte) (int, error) {
	n, err := io.ReadFull(f, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	return n, err
}

func cueQuoted(rest string) string {
	rest = pyStrip(rest)
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : end+1]
		}
		return rest[1:]
	}
	if f := pySplit(rest); len(f) > 0 {
		return f[0]
	}
	return ""
}

var (
	msfRE      = regexp.MustCompile(`^(\d+):(\d{1,2}):(\d{1,2})$`)
	fileLineRE = regexp.MustCompile(`^(".*?"|\S+)\s*(\S*)`)
	dateYearRE = regexp.MustCompile(`(?:19|20)\d{2}`)
)

func msf(text string) (float64, error) {
	m := msfRE.FindStringSubmatch(pyStrip(text))
	if m == nil {
		return 0, cueErr("bad INDEX time '%s'", text)
	}
	mm, _ := strconv.Atoi(m[1])
	ss, _ := strconv.Atoi(m[2])
	ff, _ := strconv.Atoi(m[3])
	return float64(mm*60+ss) + float64(ff)/sectorsPerSecond, nil
}

func findCueFile(cue, name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = name[strings.LastIndex(name, "/")+1:] // sheets written on Windows may carry a path
	dir := filepath.Dir(cue)
	direct := filepath.Join(dir, name)
	if st, err := os.Stat(direct); err == nil && st.Mode().IsRegular() {
		return direct
	}
	lowered := pyLower(name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries { // tolerate a different letter case
		if pyLower(e.Name()) == lowered {
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return p
			}
		}
	}
	return ""
}

// splitLines is str.splitlines().
func splitLines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			return true
		}
		return false
	})
}

// parseCue reads a cue sheet, or says why the image cannot be used.
func parseCue(cue string) (*CueInfo, error) {
	text, err := readCueText(cue)
	if err != nil {
		return nil, err
	}
	info := &CueInfo{Text: text}
	type file struct{ name, kind string }
	var files []file
	var cur *Track
	for _, line := range splitLines(text) {
		line = pyStrip(line)
		if line == "" {
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		key = strings.ToUpper(key)
		switch key {
		case "FILE":
			m := fileLineRE.FindStringSubmatch(pyStrip(rest))
			if m == nil {
				return nil, cueErr("bad FILE line")
			}
			files = append(files, file{cueQuoted(m[1]), strings.ToUpper(m[2])})
		case "TRACK":
			parts := pySplit(rest)
			if len(parts) < 2 {
				return nil, cueErr("bad TRACK line")
			}
			if strings.ToUpper(parts[1]) != "AUDIO" {
				return nil, cueErr("track %s is %s, only audio tracks are supported", parts[0], parts[1])
			}
			info.Tracks = append(info.Tracks, Track{N: len(info.Tracks) + 1})
			cur = &info.Tracks[len(info.Tracks)-1]
		case "TITLE":
			if cur == nil {
				info.Album = cueQuoted(rest)
			} else {
				cur.Title = cueQuoted(rest)
			}
		case "PERFORMER":
			if cur == nil {
				info.Artist = cueQuoted(rest)
			} else {
				cur.Artist = cueQuoted(rest)
			}
		case "INDEX":
			if cur != nil {
				parts := pySplit(rest)
				if len(parts) >= 2 && parts[0] == "01" {
					t, err := msf(parts[1])
					if err != nil {
						return nil, err
					}
					cur.Start, cur.hasIdx = t, true
				}
			}
		case "REM":
			sub, val, _ := strings.Cut(pyStrip(rest), " ")
			switch strings.ToUpper(sub) {
			case "GENRE":
				info.Genre = cueQuoted(val)
			case "DATE":
				info.Year = dateYearRE.FindString(val)
			}
		}
		// the slice may have grown: keep cur pointing at the last track
		if cur != nil && len(info.Tracks) > 0 {
			cur = &info.Tracks[len(info.Tracks)-1]
		}
	}
	if len(files) == 0 {
		return nil, cueErr("no FILE entry")
	}
	if len(files) > 1 {
		return nil, cueErr("images split into one file per track are not supported yet")
	}
	if len(info.Tracks) == 0 {
		return nil, cueErr("no tracks")
	}
	for _, t := range info.Tracks {
		if !t.hasIdx {
			return nil, cueErr("a track has no INDEX 01")
		}
	}
	if !sort.SliceIsSorted(info.Tracks, func(i, j int) bool { return info.Tracks[i].Start < info.Tracks[j].Start }) {
		return nil, cueErr("tracks are not in order")
	}
	name, ftype := files[0].name, files[0].kind
	target := findCueFile(cue, name)
	if target == "" {
		return nil, cueErr("image file '%s' not found next to the cue sheet", name)
	}
	info.Raw = ftype == "BINARY" || ftype == "MOTOROLA"
	if !info.Raw && !audioFileTypes[ftype] {
		return nil, cueErr("unsupported file type %s", ftype)
	}
	if info.Raw {
		st, err := os.Stat(target)
		if err != nil {
			return nil, err
		}
		d := float64(st.Size()) / bytesPerSecond
		info.Duration = &d
		if info.Tracks[len(info.Tracks)-1].Start >= d {
			return nil, cueErr("the last track starts after the end of the image file")
		}
	}
	info.Bin = target
	info.ByteOrder = "s16l"
	if ftype == "MOTOROLA" {
		info.ByteOrder = "s16b"
	}
	return info, nil
}

// trackSpans gives the tracks with start, end and length. total overrides the duration (for
// images that are not raw, whose length only VLC knows).
func trackSpans(tracks []Track, total *float64) []Span {
	out := make([]Span, len(tracks))
	for i, t := range tracks {
		end := total
		if i+1 < len(tracks) {
			e := tracks[i+1].Start
			end = &e
		}
		title := t.Title
		if title == "" {
			title = fmt.Sprintf("Track %d", t.N)
		}
		var length *float64
		if end != nil {
			l := *end - t.Start
			if l < 0 {
				l = 0
			}
			length = &l
		}
		out[i] = Span{N: t.N, Title: title, Artist: t.Artist, Start: t.Start, End: end, Length: length}
	}
	return out
}

// spansTracks turns spans back into tracks (to compute them again with VLC's length).
func spansTracks(spans []Span) []Track {
	out := make([]Track, len(spans))
	for i, s := range spans {
		out[i] = Track{N: s.N, Title: s.Title, Artist: s.Artist, Start: s.Start, hasIdx: true}
	}
	return out
}

// locate gives the index of the track that holds position (whole seconds from the start).
func locate(spans []Span, position int64) int {
	idx := 0
	for i, t := range spans {
		if position >= int64(t.Start) {
			idx = i
		}
	}
	return idx
}
