// Package media reads the media folders: DVD images (.iso) in DVD_DIR, Blu-ray images (.iso) in BLURAY_DIR,
// audio CD images (.cue + .bin) in CD_DIR. It implements ports.MediaScanner.
package media

import (
	"crypto/sha1"
	"encoding/binary"
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

// Scanner reads DVDDir, BlurayDir and CDDir.
type Scanner struct {
	DVDDir, CDDir, BlurayDir string

	warnMu sync.Mutex
	warned map[string]bool // cue sheets already reported as unusable
}

func New(dvdDir, cdDir, blurayDir string) *Scanner {
	return &Scanner{DVDDir: dvdDir, CDDir: cdDir, BlurayDir: blurayDir, warned: map[string]bool{}}
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

// udfVolume tells whether an image is a Blu-ray and reads its UDF primary volume descriptor.
//
// A Blu-ray image holds only UDF 2.x, announced by an "NSR03" descriptor in the volume recognition
// sequence (from sector 16); a DVD holds ISO 9660 plus UDF 1.x ("NSR02"). The primary volume descriptor
// (tag 1) is found through the anchor at sector 256; its 512 bytes carry the volume name and recording
// time, so they also fingerprint the disc. pvd is nil if it cannot be read.
func udfVolume(path string) (bluray bool, pvd []byte) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	sector := make([]byte, 2048)
	read := func(n int64) bool {
		got, _ := f.ReadAt(sector, n*2048)
		return got == 2048
	}
	for n := int64(16); n < 32 && read(n); n++ {
		id := string(sector[1:6])
		if id == "NSR03" {
			bluray = true
		}
		if sector[0] == 0 && id == "\x00\x00\x00\x00\x00" {
			break // end of the sequence
		}
	}
	if !bluray || !read(256) || binary.LittleEndian.Uint16(sector[0:2]) != 2 {
		return bluray, nil
	}
	length := binary.LittleEndian.Uint32(sector[16:20])
	start := int64(binary.LittleEndian.Uint32(sector[20:24]))
	for n := start; n < start+int64(length/2048) && n < start+64 && read(n); n++ {
		switch binary.LittleEndian.Uint16(sector[0:2]) {
		case 1: // primary volume descriptor
			return true, append([]byte{}, sector[:512]...)
		case 8: // terminating descriptor
			return true, nil
		}
	}
	return true, nil
}

// udfLabel is the volume name of a UDF primary volume descriptor (a 32-byte dstring at offset 24: a
// compression byte, 8 for one byte per character or 16 for UCS-2, then the characters; the last byte is the
// length used).
func udfLabel(pvd []byte) string {
	if len(pvd) < 56 {
		return ""
	}
	d := pvd[24:56]
	n := int(d[31])
	if n < 1 || n > 31 {
		return ""
	}
	var b strings.Builder
	switch d[0] {
	case 8:
		for _, c := range d[1:n] {
			if c >= 0x20 && c < 0x80 {
				b.WriteByte(c)
			}
		}
	case 16:
		for i := 1; i+1 < n; i += 2 {
			if r := rune(d[i])<<8 | rune(d[i+1]); r >= 0x20 {
				b.WriteRune(r)
			}
		}
	}
	return compat.Strip(b.String())
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

// isoRoot is one folder of disc images, and the prefix that keeps its disc IDs apart from another folder's.
type isoRoot struct{ dir, idPrefix string }

// isoRoots are DVD_DIR and BLURAY_DIR, once each: with MEDIA_DIR they are the same folder. Each image is
// told apart by its content, so a Blu-ray in DVD_DIR (or a DVD in BLURAY_DIR) still opens as what it is.
func (s *Scanner) isoRoots() []isoRoot {
	roots := []isoRoot{{dir: s.DVDDir}}
	if s.BlurayDir != "" && filepath.Clean(s.BlurayDir) != filepath.Clean(s.DVDDir) {
		roots = append(roots, isoRoot{dir: s.BlurayDir, idPrefix: "bluray/"})
	}
	return roots
}

// scanISO turns one DVD or Blu-ray image into a disc entry.
func scanISO(root isoRoot, p string, perDir map[string]int) *domain.Disc {
	rel := relPosix(root.dir, p)
	id := sha1Hex([]byte(root.idPrefix + rel))[:12]
	format, state := "", p
	label := ""
	pvd := readPVD(p)
	if pvd != nil {
		var b strings.Builder
		for _, c := range pvd[40:72] {
			if c < 0x80 {
				b.WriteByte(c)
			}
		}
		label = compat.Strip(b.String())
	}
	if bluray, udf := udfVolume(p); bluray {
		format, state = "bluray", "bluray:"+p
		if udf != nil {
			pvd = udf
			if l := udfLabel(udf); l != "" {
				label = l
			}
		}
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
		if perDir[parent] == 1 && filepath.Clean(parent) != filepath.Clean(root.dir) {
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
	return &domain.Disc{
		ID:        id,
		FP:        sha1Hex(fpSrc)[:16],
		Kind:      "dvd",
		Format:    format,
		Path:      p,
		State:     state,
		Rel:       rel,
		Title:     title,
		Year:      year,
		Label:     label,
		Size:      size,
		Added:     added,
		UserCover: findUserCover(p, perDir[filepath.Dir(p)]),
	}
}

// Scan reads DVD_DIR and BLURAY_DIR for ISOs and CD_DIR for cue sheets.
func (s *Scanner) Scan() *domain.Catalog {
	roots := s.isoRoots()
	isos := make([][]string, len(roots))
	cues := walkFiles(s.CDDir, ".cue")
	perDir := map[string]int{}
	for i, r := range roots {
		isos[i] = walkFiles(r.dir, ".iso")
		for _, p := range isos[i] {
			perDir[filepath.Dir(p)]++
		}
	}
	for _, p := range cues {
		perDir[filepath.Dir(p)]++
	}
	found := domain.NewCatalog()
	for _, p := range cues {
		if d := s.scanCue(p, perDir); d != nil {
			found.Put(d)
		}
	}
	for i, r := range roots {
		for _, p := range isos[i] {
			found.Put(scanISO(r, p, perDir))
		}
	}
	return found
}

var (
	imageExts        = []string{".jpg", ".jpeg", ".png", ".webp"}
	folderCoverNames = []string{"cover", "folder", "poster", "front"}
)
