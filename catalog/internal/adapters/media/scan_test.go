package media

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeImage writes a sparse disc image whose sectors (2048 bytes) start with the given bytes.
func writeImage(t *testing.T, path string, sectors map[int64][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(300 * 2048); err != nil {
		t.Fatal(err)
	}
	for n, data := range sectors {
		if _, err := f.WriteAt(data, n*2048); err != nil {
			t.Fatal(err)
		}
	}
}

func descriptor(id string) []byte { return append([]byte{0}, id...) }

// dvdImage has ISO 9660 (with a volume label) and UDF 1.x, as DVD images do.
func dvdImage(label string) map[int64][]byte {
	pvd := make([]byte, 2048)
	copy(pvd, "\x01CD001")
	copy(pvd[40:72], label+"                                ")
	return map[int64][]byte{16: pvd, 17: []byte("\xffCD001"), 18: descriptor("BEA01"), 19: descriptor("NSR02"), 20: descriptor("TEA01")}
}

// blurayImage has only UDF 2.x, with an anchor at sector 256 pointing to a primary volume descriptor.
func blurayImage(label string) map[int64][]byte {
	anchor := make([]byte, 512)
	binary.LittleEndian.PutUint16(anchor[0:2], 2)
	binary.LittleEndian.PutUint32(anchor[16:20], 16*2048) // main volume descriptor sequence: length...
	binary.LittleEndian.PutUint32(anchor[20:24], 32)      // ...and first sector
	pvd := make([]byte, 512)
	binary.LittleEndian.PutUint16(pvd[0:2], 1)
	pvd[24] = 8 // one byte per character
	copy(pvd[25:], label)
	pvd[55] = byte(len(label) + 1)
	term := make([]byte, 16)
	binary.LittleEndian.PutUint16(term[0:2], 8)
	return map[int64][]byte{16: descriptor("BEA01"), 17: descriptor("NSR03"), 18: descriptor("TEA01"), 256: anchor, 32: pvd, 33: term}
}

func TestScanTellsBlurayFromDVD(t *testing.T) {
	root := t.TempDir()
	dvds, blurays := filepath.Join(root, "dvds"), filepath.Join(root, "blurays")
	writeImage(t, filepath.Join(dvds, "Movie.iso"), dvdImage("MOVIE_DVD"))
	writeImage(t, filepath.Join(blurays, "Movie.iso"), blurayImage("CLOUDY_3D"))
	writeImage(t, filepath.Join(dvds, "Mixed In.iso"), blurayImage("MIXED_BD")) // a Blu-ray among the DVDs

	discs := New(dvds, filepath.Join(root, "cds"), blurays).Scan().Values()
	if len(discs) != 3 {
		t.Fatalf("found %d discs, want 3", len(discs))
	}
	byPath := map[string]int{}
	ids := map[string]bool{}
	for i, d := range discs {
		byPath[d.Path] = i
		ids[d.ID] = true
	}
	if len(ids) != 3 {
		t.Errorf("the same file name in both folders must give different IDs")
	}
	cases := []struct{ path, format, state, label string }{
		{filepath.Join(dvds, "Movie.iso"), "", filepath.Join(dvds, "Movie.iso"), "MOVIE_DVD"},
		{filepath.Join(blurays, "Movie.iso"), "bluray", "bluray:" + filepath.Join(blurays, "Movie.iso"), "CLOUDY_3D"},
		{filepath.Join(dvds, "Mixed In.iso"), "bluray", "bluray:" + filepath.Join(dvds, "Mixed In.iso"), "MIXED_BD"},
	}
	for _, c := range cases {
		i, ok := byPath[c.path]
		if !ok {
			t.Errorf("%s not found", c.path)
			continue
		}
		d := discs[i]
		if d.Kind != "dvd" || d.Format != c.format || d.State != c.state || d.Label != c.label {
			t.Errorf("%s: kind=%q format=%q state=%q label=%q, want dvd %q %q %q",
				c.path, d.Kind, d.Format, d.State, d.Label, c.format, c.state, c.label)
		}
	}
}

func TestScanOnceWhenFoldersAreTheSame(t *testing.T) {
	media := t.TempDir()
	writeImage(t, filepath.Join(media, "A.iso"), dvdImage("A"))
	writeImage(t, filepath.Join(media, "B.iso"), blurayImage("B"))
	if n := New(media, media, media).Scan().Len(); n != 2 {
		t.Errorf("MEDIA_DIR for everything: found %d discs, want 2", n)
	}
}

func TestUDFLabelInUCS2(t *testing.T) {
	pvd := make([]byte, 512)
	pvd[24] = 16
	name := []byte{0, 'B', 0, 'D'}
	copy(pvd[25:], name)
	pvd[55] = byte(len(name) + 1)
	if got := udfLabel(pvd); got != "BD" {
		t.Errorf("udfLabel = %q, want BD", got)
	}
}
