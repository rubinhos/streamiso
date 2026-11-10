package main

// Reading the media folders: DVD images (.iso) in DVD_DIR, audio CD images (.cue + .bin) in CD_DIR.

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Disc is one entry of the catalog. Year and UserCover are "" when unknown.
type Disc struct {
	ID         string
	FP         string
	Kind       string
	Path       string
	State      string
	Rel        string
	Title      string
	Artist     string
	Year       string
	Label      string
	Size       int64
	Added      int64
	Raw        bool
	Duration   *float64
	Tracks     []Span
	GenresAuto []string
	UserCover  string

	mu         sync.Mutex
	coverFound *bool // set by a CD lookup, read once by /api/disc/<id>/lookup
}

var (
	imageExts        = []string{".jpg", ".jpeg", ".png", ".webp"}
	folderCoverNames = []string{"cover", "folder", "poster", "front"}
)

func sha1Hex(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// readPVD reads the ISO 9660 primary volume descriptor (sector 16, 2048 bytes); nil if not an ISO.
func readPVD(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data := make([]byte, 2048)
	n, err := f.ReadAt(data, 0x8000)
	if n != 2048 || (err != nil && n != 2048) || string(data[1:6]) != "CD001" {
		return nil
	}
	return data
}

// withSuffix is Path.with_suffix: the path with its last suffix replaced.
func withSuffix(p, ext string) string {
	dir, name := filepath.Split(p)
	return dir + pyStem(name) + ext
}

func findUserCover(media string, inDir int) string {
	for _, ext := range imageExts {
		if c := withSuffix(media, ext); isFile(c) {
			return c
		}
		if c := withSuffix(media, ".cover"+ext); isFile(c) {
			return c
		}
	}
	if inDir == 1 { // a folder cover only makes sense when the folder has a single disc
		for _, name := range folderCoverNames {
			for _, ext := range imageExts {
				if c := filepath.Join(filepath.Dir(media), name+ext); isFile(c) {
					return c
				}
			}
		}
	}
	return ""
}

// walkFiles lists the files under root with the given suffix (any letter case), sorted as Python
// sorts paths: component by component.
func walkFiles(root, suffix string) []string {
	if !isDir(root) {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root || d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(d.Name())) == suffix && pyStem(d.Name()) != d.Name() && isFile(p) {
			out = append(out, p)
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return lessPath(out[i], out[j]) })
	return out
}

func lessPath(a, b string) bool {
	pa := strings.Split(filepath.ToSlash(a), "/")
	pb := strings.Split(filepath.ToSlash(b), "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func relPosix(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

var (
	cueWarnedMu sync.Mutex
	cueWarned   = map[string]bool{}
)

func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r, nil
	}
	return abs, nil
}

// scanCue turns one audio CD image (BIN/CUE) into a disc entry, or nil if the sheet is unusable.
func scanCue(p string, perDir map[string]int) *Disc {
	fail := func(err error) *Disc {
		var mtime int64
		if st, e := os.Stat(p); e == nil {
			mtime = st.ModTime().Unix()
		}
		key := p + "\x00" + strconv.FormatInt(mtime, 10)
		cueWarnedMu.Lock()
		defer cueWarnedMu.Unlock()
		if !cueWarned[key] {
			cueWarned[key] = true
			log.Printf("WARNING: Skipping %s: %v", p, err)
		}
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return fail(err)
	}
	info, err := parseCue(p)
	if err != nil {
		return fail(err)
	}
	binPath, err := resolvePath(info.Bin)
	if err != nil {
		return fail(err)
	}
	cdRoot, err := resolvePath(cfg.CDDir)
	if err != nil {
		return fail(err)
	}
	if rel, err := filepath.Rel(cdRoot, binPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fail(cueErr("'%s' is not in the subpath of '%s'", binPath, cdRoot)) // the image must live inside the CD folder
	}
	bst, err := os.Stat(binPath)
	if err != nil {
		return fail(err)
	}
	rel := relPosix(cfg.CDDir, p)
	title, year := cleanTitle(pyStem(rel))
	if info.Album != "" {
		title = info.Album
	}
	if info.Year != "" {
		year = info.Year
	}
	spans := trackSpans(info.Tracks, info.Duration)
	state := "file:" + binPath
	if info.Raw {
		state = "cd-" + info.ByteOrder + ":" + binPath
	}
	var genres []string
	if info.Genre != "" {
		genres = []string{info.Genre}
	}
	return &Disc{
		ID:         sha1Hex([]byte(rel))[:12],
		FP:         sha1Hex(append([]byte(info.Text), []byte(strconv.FormatInt(bst.Size(), 10))...))[:16],
		Kind:       "cd",
		Path:       p,
		State:      state,
		Rel:        rel,
		Title:      title,
		Artist:     info.Artist,
		Year:       year,
		Size:       bst.Size(),
		Added:      st.ModTime().Unix(),
		Raw:        info.Raw,
		Duration:   info.Duration,
		Tracks:     spans,
		GenresAuto: genres,
		UserCover:  findUserCover(p, perDir[filepath.Dir(p)]),
	}
}

// Catalog keeps the discs in the order Python's dict does: by first insertion.
type Catalog struct {
	order []string
	byID  map[string]*Disc
}

func (c *Catalog) put(d *Disc) {
	if _, ok := c.byID[d.ID]; !ok {
		c.order = append(c.order, d.ID)
	}
	c.byID[d.ID] = d
}

func (c *Catalog) values() []*Disc {
	out := make([]*Disc, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.byID[id])
	}
	return out
}

// scan reads DVD_DIR for ISOs and CD_DIR for cue sheets.
func scan() *Catalog {
	isos := walkFiles(cfg.DVDDir, ".iso")
	cues := walkFiles(cfg.CDDir, ".cue")
	perDir := map[string]int{}
	for _, p := range append(append([]string{}, isos...), cues...) {
		perDir[filepath.Dir(p)]++
	}
	found := &Catalog{byID: map[string]*Disc{}}
	for _, p := range cues {
		if d := scanCue(p, perDir); d != nil {
			found.put(d)
		}
	}
	dvdRoot := filepath.Clean(cfg.DVDDir)
	for _, p := range isos {
		rel := relPosix(cfg.DVDDir, p)
		id := sha1Hex([]byte(rel))[:12]
		pvd := readPVD(p)
		label := ""
		if pvd != nil {
			var b strings.Builder
			for _, c := range pvd[40:72] {
				if c < 0x80 {
					b.WriteByte(c)
				}
			}
			label = pyStrip(b.String())
		}
		var size, added int64
		if st, err := os.Stat(p); err == nil {
			size, added = st.Size(), st.ModTime().Unix()
		}
		// Fingerprint: the volume descriptor (label, creation date, volume size...) plus the file
		// size. Survives renaming/moving the ISO.
		var fpSrc []byte
		if pvd != nil {
			fpSrc = append(append([]byte{}, pvd...), strconv.FormatInt(size, 10)...)
		} else {
			fpSrc = []byte(rel + strconv.FormatInt(size, 10))
		}
		title, year := cleanTitle(pyStem(p))
		if isGeneric(title) {
			// generic file name: try the folder name (if it holds a single ISO), then the volume label
			parent := filepath.Dir(p)
			if perDir[parent] == 1 && filepath.Clean(parent) != dvdRoot {
				ft, fy := cleanTitle(filepath.Base(parent))
				if ft != "" && !isGeneric(ft) {
					title = ft
					if fy != "" {
						year = fy
					}
				}
			}
			if isGeneric(title) {
				lt, ly := cleanTitle(label)
				if lt != "" && !isGeneric(lt) {
					title = lt
					if year == "" {
						year = ly
					}
				} else if title == "" {
					title = pyStem(p)
				}
			}
		}
		found.put(&Disc{
			ID:        id,
			FP:        sha1Hex(fpSrc)[:16],
			Kind:      "dvd",
			Path:      p,
			State:     p,
			Rel:       rel,
			Title:     title,
			Year:      year,
			Label:     label,
			Size:      size,
			Added:     added,
			UserCover: findUserCover(p, perDir[filepath.Dir(p)]),
		})
	}
	return found
}
