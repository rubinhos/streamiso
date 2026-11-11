// StreamISO's catalog service.
//
// Scans a folder for DVD images (.iso) and one for audio CD images (.cue + .bin), builds the library with
// covers (custom cover > image next to the disc > TMDB or the Cover Art Archive > generated placeholder),
// and stores the user's edits in DATA_DIR/library.json, tied to a fingerprint of each image so renaming
// or moving a file does not lose them.
//
// To play a disc it writes the image's path to a state file that the player container (Neko, running
// VLC) watches, and drives VLC through its remote-control interface. Neko streams the screen over WebRTC,
// and as plain HLS video for TVs whose WebRTC cannot connect.
//
// This file only puts the pieces together: the adapters (internal/adapters) behind the ports
// (internal/ports), the use cases (internal/app) and the HTTP API (internal/httpapi).
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"streamiso/internal/adapters/fsstore"
	"streamiso/internal/adapters/media"
	"streamiso/internal/adapters/musicbrainz"
	"streamiso/internal/adapters/neko"
	"streamiso/internal/adapters/tmdb"
	"streamiso/internal/adapters/vlc"
	"streamiso/internal/app"
	"streamiso/internal/config"
	"streamiso/internal/httpapi"
)

func main() {
	log.SetFlags(log.LstdFlags)
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	store := fsstore.New(cfg)
	if err := store.DropSeriesEdits(); err != nil { // read-only or missing data folder: the field is ignored anyway
		log.Printf("WARNING: Could not clean up library.json: %v", err)
	}
	player := vlc.New(cfg.VLCHost, cfg.VLCPort, 2*time.Second)
	movies := &tmdb.Client{API: cfg.TMDBAPI, ImageURL: cfg.TMDBImg, Key: cfg.TMDBKey}
	music := &musicbrainz.Client{URL: cfg.MBURL, CAAURL: cfg.CAAURL, Contact: cfg.MBContact}
	video := neko.New(cfg.HLSDir, cfg.NekoInternalURL, cfg.NekoAdminPass)

	catalog := app.NewCatalog(media.New(cfg.DVDDir, cfg.CDDir, cfg.BlurayDir))
	metadata := app.NewMetadata(app.MetadataConfig{TMDBKey: cfg.TMDBKey, TMDBLanguage: cfg.TMDBLanguage},
		movies, music, store.Metadata(), store.Library(), store.Covers(), catalog)
	// DVDs are looked up on TMDB (needs a key), CDs on MusicBrainz (no key), after each scan
	catalog.OnScan = metadata.EnrichAll
	library := app.NewLibrary(store.Library(), store.Metadata(), store.Resume(), store.Covers(), store.State(), catalog, metadata)
	playback := app.NewPlayback(player, store.State(), store.Resume(), store.Covers(), video, catalog, library)
	if bw, bh, ok := neko.ParseSize(cfg.BlurayResolution); ok {
		if dw, dh, ok := neko.ParseSize(cfg.StreamResolution); ok {
			playback.UseScreen(video, [2]int{dw, dh}, [2]int{bw, bh})
		} else {
			log.Printf("WARNING: BLURAY_RESOLUTION needs STREAM_RESOLUTION too: the screen keeps one size")
		}
	} else if cfg.BlurayResolution != "" {
		log.Printf("WARNING: BLURAY_RESOLUTION %q is not WIDTHxHEIGHT: the screen keeps one size", cfg.BlurayResolution)
	}

	api := httpapi.New(cfg, catalog, library, metadata, playback)
	addr := "0.0.0.0:" + cfg.Port
	log.Printf("StreamISO catalog %s listening at http://%s", config.Version, addr)
	srv := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 30 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
