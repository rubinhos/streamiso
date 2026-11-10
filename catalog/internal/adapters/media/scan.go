// Package media reads the media folders: DVD images (.iso) in DVD_DIR, audio CD images (.cue + .bin) in
// CD_DIR. It implements ports.MediaScanner.
package media

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"strings"
	"sync"
)

// Scanner reads DVDDir and CDDir.
type Scanner struct {
	DVDDir, CDDir string

	warnMu sync.Mutex
	warned map[string]bool // cue sheets already reported as unusable
}

func New(dvdDir, cdDir string) *Scanner {
	return &Scanner{DVDDir: dvdDir, CDDir: cdDir, warned: map[string]bool{}}
}

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
	return dir + compat.Stem(name) + ext
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
		if strings.ToLower(filepath.Ext(d.Name())) == suffix && compat.Stem(d.Name()) != d.Name() && isFile(p) {
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
func (s *Scanner) scanCue(p string, perDir map[string]int) *domain.Disc {
	fail := func(err error) *domain.Disc {
		var mtime int64
		if st, e := os.Stat(p); e == nil {
			mtime = st.ModTime().Unix()
		}
		key := p + "\x00" + strconv.FormatInt(mtime, 10)
		s.warnMu.Lock()
		defer s.warnMu.Unlock()
		if !s.warned[key] {
			s.warned[key] = true
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
	cdRoot, err := resolvePath(s.CDDir)
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
	rel := relPosix(s.CDDir, p)
	title, year := domain.CleanTitle(compat.Stem(rel))
	if info.Album != "" {
		title = info.Album
	}
	if info.Year != "" {
		year = info.Year
	}
	spans := domain.TrackSpans(info.Tracks, info.Duration)
	state := "file:" + binPath
	if info.Raw {
		state = "cd-" + info.ByteOrder + ":" + binPath
	}
	var genres []string
	if info.Genre != "" {
		genres = []string{info.Genre}
	}
	return &domain.Disc{
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

// Scan reads DVD_DIR for ISOs and CD_DIR for cue sheets.
func (s *Scanner) Scan() *domain.Catalog {
	isos := walkFiles(s.DVDDir, ".iso")
	cues := walkFiles(s.CDDir, ".cue")
	perDir := map[string]int{}
	for _, p := range append(append([]string{}, isos...), cues...) {
		perDir[filepath.Dir(p)]++
	}
	found := domain.NewCatalog()
	for _, p := range cues {
		if d := s.scanCue(p, perDir); d != nil {
			found.Put(d)
		}
	}
	dvdRoot := filepath.Clean(s.DVDDir)
	for _, p := range isos {
		rel := relPosix(s.DVDDir, p)
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
			label = compat.Strip(b.String())
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
		title, year := domain.CleanTitle(compat.Stem(p))
		if domain.IsGeneric(title) {
			// generic file name: try the folder name (if it holds a single ISO), then the volume label
			parent := filepath.Dir(p)
			if perDir[parent] == 1 && filepath.Clean(parent) != dvdRoot {
				ft, fy := domain.CleanTitle(filepath.Base(parent))
				if ft != "" && !domain.IsGeneric(ft) {
					title = ft
					if fy != "" {
						year = fy
					}
				}
			}
			if domain.IsGeneric(title) {
				lt, ly := domain.CleanTitle(label)
				if lt != "" && !domain.IsGeneric(lt) {
					title = lt
					if year == "" {
						year = ly
					}
				} else if title == "" {
					title = compat.Stem(p)
				}
			}
		}
		found.Put(&domain.Disc{
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

var (
	imageExts        = []string{".jpg", ".jpeg", ".png", ".webp"}
	folderCoverNames = []string{"cover", "folder", "poster", "front"}
)
