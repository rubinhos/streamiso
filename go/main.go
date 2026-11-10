// StreamISO - catalog service (Go version).
//
// Scans a folder for DVD .iso images, reads each volume label, builds the library with covers
// (custom cover > image next to the ISO > TMDB > generated placeholder) and stores the user's
// edits (title, year, genres, synopsis, season/disc, watched) in DATA_DIR/library.json.
//
// Edits are tied to a fingerprint of the ISO (not to its path), so renaming or moving a file does
// not lose them.
//
// To play a disc, the catalog writes the ISO path to a state file that the Neko container
// (running VLC) watches. The player screen is streamed by Neko over WebRTC.
//
// It answers exactly like the Python version in ../catalog, with the same API and data files.
package main

import (
	"log"
	"mime"
	"net/http"
	"os"
	"sync"
	"time"
)

const rescanTTL = 30 * time.Second // between automatic rescans

var (
	catMu     sync.Mutex
	catalog   = &Catalog{byID: map[string]*Disc{}}
	scannedAt time.Time
)

// getCatalog rescans the folders when the last scan is older than rescanTTL (or when forced), and
// then looks up what is new in the background.
func getCatalog(force bool) *Catalog {
	catMu.Lock()
	defer catMu.Unlock()
	if force || time.Since(scannedAt) > rescanTTL {
		catalog = scan()
		scannedAt = time.Now()
		// DVDs are looked up on TMDB (needs a key), CDs on MusicBrainz (no key)
		go enrichAll()
	}
	return catalog
}

// currentDiscs is the catalog as last scanned, without scanning.
func currentDiscs() []*Disc {
	catMu.Lock()
	defer catMu.Unlock()
	return catalog.values()
}

func main() {
	log.SetFlags(log.LstdFlags)
	loadConfig()
	vlc = &VlcRc{host: cfg.VLCHost, port: cfg.VLCPort, timeout: 2 * time.Second}
	for ext, typ := range map[string]string{
		".ico": "image/vnd.microsoft.icon", ".webmanifest": "application/manifest+json",
		".svg": "image/svg+xml", ".webp": "image/webp", ".js": "text/javascript; charset=utf-8",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
	if err := dropSeriesEdits(); err != nil { // read-only or missing data folder: the field is ignored anyway
		log.Printf("WARNING: Could not clean up library.json: %v", err)
	}
	addr := "0.0.0.0:" + cfg.Port
	log.Printf("StreamISO catalog %s (Go) listening at http://%s", version, addr)
	srv := &http.Server{Addr: addr, Handler: routes(), ReadHeaderTimeout: 30 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
