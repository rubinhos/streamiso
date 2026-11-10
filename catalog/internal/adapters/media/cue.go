// Audio CD images: reads .cue sheets (BIN/CUE).
//
// Only the common "one image file" layout is supported: a single FILE entry that holds every
// track, all of them AUDIO. Raw BINARY images are 44.1 kHz, 16-bit, stereo, 2352-byte sectors,
// 75 sectors per second. VLC plays them directly as one continuous stream, and a track is just a
// position in it (which also gives gapless playback, like a real CD).

package media

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"strings"
	"unicode/utf8"
)

var audioFileTypes = map[string]bool{"WAVE": true, "MP3": true, "AIFF": true, "FLAC": true}

type CueError struct{ msg string }

func cueErr(format string, a ...any) error { return &CueError{fmt.Sprintf(format, a...)} }

type CueInfo struct {
	Text      string
	Album     string
	Artist    string
	Year      string
	Genre     string
	Bin       string
	ByteOrder string
	Raw       bool
	Tracks    []domain.Track
	Duration  *float64
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
	rest = compat.Strip(rest)
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : end+1]
		}
		return rest[1:]
	}
	if f := compat.Split(rest); len(f) > 0 {
		return f[0]
	}
	return ""
}

func msf(text string) (float64, error) {
	m := msfRE.FindStringSubmatch(compat.Strip(text))
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
	lowered := compat.Lower(name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries { // tolerate a different letter case
		if compat.Lower(e.Name()) == lowered {
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return p
			}
		}
	}
	return ""
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
	var cur *domain.Track
	for _, line := range compat.SplitLines(text) {
		line = compat.Strip(line)
		if line == "" {
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		key = strings.ToUpper(key)
		switch key {
		case "FILE":
			m := fileLineRE.FindStringSubmatch(compat.Strip(rest))
			if m == nil {
				return nil, cueErr("bad FILE line")
			}
			files = append(files, file{cueQuoted(m[1]), strings.ToUpper(m[2])})
		case "TRACK":
			parts := compat.Split(rest)
			if len(parts) < 2 {
				return nil, cueErr("bad TRACK line")
			}
			if strings.ToUpper(parts[1]) != "AUDIO" {
				return nil, cueErr("track %s is %s, only audio tracks are supported", parts[0], parts[1])
			}
			info.Tracks = append(info.Tracks, domain.Track{N: len(info.Tracks) + 1})
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
				parts := compat.Split(rest)
				if len(parts) >= 2 && parts[0] == "01" {
					t, err := msf(parts[1])
					if err != nil {
						return nil, err
					}
					cur.Start, cur.HasIndex = t, true
				}
			}
		case "REM":
			sub, val, _ := strings.Cut(compat.Strip(rest), " ")
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
		if !t.HasIndex {
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

func (e *CueError) Error() string { return e.msg }

var (
	msfRE      = regexp.MustCompile(`^(\d+):(\d{1,2}):(\d{1,2})$`)
	fileLineRE = regexp.MustCompile(`^(".*?"|\S+)\s*(\S*)`)
	dateYearRE = regexp.MustCompile(`(?:19|20)\d{2}`)
)

const (
	sectorsPerSecond = 75
	bytesPerSecond   = 44100 * 2 * 2 // 44.1 kHz * 16 bit * stereo = 176400
)
