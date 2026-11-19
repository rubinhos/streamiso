package media

// Reading a DVD image's menus: the ISO 9660 file system finds VIDEO_TS, and its IFO files list the titles and
// which menus each part of the disc has (see domain.DVDLayout). Only the IFO files are read, a few KB each.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"streamiso/internal/domain"
)

const sectorSize = 2048

// DVDReader reads the menus of DVD images, once per image (it implements ports.DVDMenus).
type DVDReader struct {
	// MenuLanguage is the player's menu language (DVD_MENU_LANGUAGE, such as "pt"). A disc may hold its menus
	// once per language; like the player, the reader takes that language's, else the first.
	MenuLanguage string

	mu    sync.Mutex
	cache map[string]*domain.DVDLayout
}

func (r *DVDReader) Layout(path string) (*domain.DVDLayout, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s|%d|%d", path, st.Size(), st.ModTime().UnixNano())
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.cache[key]; ok {
		return l, nil
	}
	l, err := ReadDVDLayout(path, r.MenuLanguage)
	if err != nil {
		return nil, err
	}
	if r.cache == nil {
		r.cache = map[string]*domain.DVDLayout{}
	}
	r.cache[key] = l
	return l, nil
}

type isoFile struct {
	name      string // upper case, without the ";1" version
	lba, size uint32
	dir       bool
}

// isoDir lists an ISO 9660 directory. Its records never cross a sector: a zero length means the next sector.
func isoDir(f io.ReaderAt, lba, size uint32) ([]isoFile, error) {
	if size > 1<<20 {
		return nil, errors.New("directory too big")
	}
	data := make([]byte, size)
	if _, err := f.ReadAt(data, int64(lba)*sectorSize); err != nil && err != io.EOF {
		return nil, err
	}
	var out []isoFile
	for pos := 0; pos < len(data); {
		n := int(data[pos])
		if n == 0 {
			pos = (pos/sectorSize + 1) * sectorSize
			continue
		}
		if n < 34 || pos+n > len(data) {
			break
		}
		rec := data[pos : pos+n]
		nameLen := int(rec[32])
		if 33+nameLen <= n {
			name := strings.ToUpper(string(rec[33 : 33+nameLen]))
			if i := strings.IndexByte(name, ';'); i >= 0 {
				name = name[:i]
			}
			out = append(out, isoFile{name: strings.TrimSuffix(name, "."), lba: binary.LittleEndian.Uint32(rec[2:]),
				size: binary.LittleEndian.Uint32(rec[10:]), dir: rec[25]&2 != 0})
		}
		pos += n
	}
	return out, nil
}

func findFile(files []isoFile, name string) (isoFile, bool) {
	for _, e := range files {
		if e.name == name {
			return e, true
		}
	}
	return isoFile{}, false
}

func readISOFile(f io.ReaderAt, e isoFile) ([]byte, error) {
	if e.size > 16<<20 {
		return nil, errors.New("IFO file too big")
	}
	data := make([]byte, e.size)
	_, err := f.ReadAt(data, int64(e.lba)*sectorSize)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return data, nil
}

// ReadDVDLayout reads the menus and titles of the DVD image at path. Images with no ISO 9660 file system
// (UDF only, which few tools write) give an error.
func ReadDVDLayout(path, menuLanguage string) (*domain.DVDLayout, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pvd := make([]byte, sectorSize)
	if _, err := f.ReadAt(pvd, 16*sectorSize); err != nil {
		return nil, err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return nil, errors.New("no ISO 9660 file system")
	}
	root, err := isoDir(f, binary.LittleEndian.Uint32(pvd[156+2:]), binary.LittleEndian.Uint32(pvd[156+10:]))
	if err != nil {
		return nil, err
	}
	vts, ok := findFile(root, "VIDEO_TS")
	if !ok || !vts.dir {
		return nil, errors.New("no VIDEO_TS folder")
	}
	files, err := isoDir(f, vts.lba, vts.size)
	if err != nil {
		return nil, err
	}
	vmgFile, ok := findFile(files, "VIDEO_TS.IFO")
	if !ok {
		return nil, errors.New("no VIDEO_TS.IFO")
	}
	vmg, err := readISOFile(f, vmgFile)
	if err != nil {
		return nil, err
	}
	layout, sets, err := parseVMG(vmg, menuLanguage)
	if err != nil {
		return nil, err
	}
	for _, n := range sets {
		e, ok := findFile(files, fmt.Sprintf("VTS_%02d_0.IFO", n))
		if !ok {
			continue
		}
		data, err := readISOFile(f, e)
		if err != nil {
			continue
		}
		if menus, err := parseVTS(data, menuLanguage); err == nil {
			layout.SetMenus[n] = menus
		}
	}
	return layout, nil
}

func u16(b []byte, at int) int {
	if at < 0 || at+2 > len(b) {
		return 0
	}
	return int(binary.BigEndian.Uint16(b[at:]))
}

func u32(b []byte, at int) int {
	if at < 0 || at+4 > len(b) {
		return 0
	}
	return int(binary.BigEndian.Uint32(b[at:]))
}

// The menu types of a PGC's entry id (its low bits, with 0x80 set on a menu's entry PGC).
var menuTypes = map[byte]string{2: domain.MenuTitle, 3: domain.MenuRoot, 4: domain.MenuSubtitles, 5: domain.MenuAudio,
	6: domain.MenuAngle, 7: domain.MenuChapters}

// menusOf reads a menu PGCI unit table (PGCI_UT) at offset at: the menus of the language unit the player
// uses, which is menuLanguage's, else the first (as libdvdnav does).
func menusOf(b []byte, at int, menuLanguage string) map[string]bool {
	out := map[string]bool{}
	if at <= 0 || at >= len(b) {
		return out
	}
	units := u16(b, at)
	if units == 0 || units > 100 {
		return out
	}
	unit := at + 8
	for i := 0; i < units; i++ {
		e := at + 8 + i*8
		if e+2 <= len(b) && menuLanguage != "" && strings.EqualFold(string(b[e:e+2]), menuLanguage) {
			unit = e
			break
		}
	}
	pgcit := at + u32(b, unit+4)
	n := u16(b, pgcit)
	for i := 0; i < n && i < 1000; i++ {
		e := pgcit + 8 + i*8
		if e >= len(b) {
			break
		}
		if id := b[e]; id&0x80 != 0 {
			if t, ok := menuTypes[id&0x0f]; ok {
				out[t] = true
			}
		}
	}
	return out
}

// parseVMG reads VIDEO_TS.IFO: whether the disc has a Title menu, and the title set of each title.
func parseVMG(b []byte, menuLanguage string) (*domain.DVDLayout, []int, error) {
	if len(b) < 0x100 || string(b[:12]) != "DVDVIDEO-VMG" {
		return nil, nil, errors.New("VIDEO_TS.IFO is not a video manager")
	}
	l := &domain.DVDLayout{SetMenus: map[int]map[string]bool{}}
	l.TitleMenu = menusOf(b, u32(b, 0xC8)*sectorSize, menuLanguage)[domain.MenuTitle]
	srpt := u32(b, 0xC4) * sectorSize
	seen := map[int]bool{}
	var sets []int
	for i, n := 0, u16(b, srpt); i < n && i < 99; i++ {
		e := srpt + 8 + i*12
		if e+12 > len(b) {
			break
		}
		set := int(b[e+6])
		l.TitleSets = append(l.TitleSets, set)
		if !seen[set] {
			seen[set] = true
			sets = append(sets, set)
		}
	}
	return l, sets, nil
}

// parseVTS reads a title set's VTS_nn_0.IFO: which menus of its own it has.
func parseVTS(b []byte, menuLanguage string) (map[string]bool, error) {
	if len(b) < 0x100 || string(b[:12]) != "DVDVIDEO-VTS" {
		return nil, errors.New("not a title set")
	}
	menus := menusOf(b, u32(b, 0xD0)*sectorSize, menuLanguage)
	delete(menus, domain.MenuTitle) // only the video manager's Title menu is the disc's
	return menus, nil
}
