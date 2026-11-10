package main

// Settings, and the files under DATA_DIR: library.json (user edits), meta/<id>.json (the TMDB or
// MusicBrainz cache), resume.json (where each disc was left), the covers.

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "0.6.0"

type config struct {
	DVDDir, CDDir, DataDir, StateFile       string
	VLCHost                                 string
	VLCPort                                 int
	NekoPort, NekoPublicURL                 string
	CORSOrigins                             []string
	NekoUser, NekoPassword                  string
	TMDBKey, TMDBLanguage, TMDBAPI, TMDBImg string
	MBURL, CAAURL, MBContact                string
	Port                                    string
	StaticDir                               string
	CoverDir, CustomCoverDir, MetaDir       string
	LibFile, ResumeFile                     string
}

var cfg config

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// pyPath is str(Path(p)): no trailing slash, no "." parts, no doubled slashes.
func pyPath(p string) string {
	if p == "" {
		return "."
	}
	return filepath.Clean(p)
}

func loadConfig() {
	media := env("MEDIA_DIR", "")
	first := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	cfg.DVDDir = pyPath(first(os.Getenv("DVD_DIR"), media, "/media/dvds"))
	cfg.CDDir = pyPath(first(os.Getenv("CD_DIR"), media, "/media/cds"))
	cfg.DataDir = pyPath(env("DATA_DIR", "/data"))
	cfg.StateFile = pyPath(env("STATE_FILE", "/state/current"))
	cfg.VLCHost = env("VLC_RC_HOST", "neko")
	port, err := strconv.Atoi(env("VLC_RC_PORT", "4212"))
	if err != nil {
		log.Fatalf("VLC_RC_PORT: %v", err)
	}
	cfg.VLCPort = port
	cfg.NekoPort = env("NEKO_PORT", "8081")
	cfg.NekoPublicURL = strings.TrimRight(env("NEKO_PUBLIC_URL", ""), "/")
	for _, o := range strings.Split(env("CORS_ORIGIN", ""), ",") {
		if o = pyStrip(o); o != "" {
			cfg.CORSOrigins = append(cfg.CORSOrigins, o)
		}
	}
	cfg.NekoUser = env("NEKO_USER", "tv")
	cfg.NekoPassword = env("NEKO_PASSWORD", "neko")
	cfg.TMDBKey = pyStrip(env("TMDB_API_KEY", ""))
	cfg.TMDBLanguage = env("TMDB_LANGUAGE", "en-US")
	cfg.TMDBAPI = strings.TrimRight(env("TMDB_API_URL", "https://api.themoviedb.org/3"), "/")
	cfg.TMDBImg = strings.TrimRight(env("TMDB_IMAGE_URL", "https://image.tmdb.org/t/p/w500"), "/")
	cfg.MBURL = strings.TrimRight(env("MUSICBRAINZ_URL", "https://musicbrainz.org/ws/2"), "/")
	cfg.CAAURL = strings.TrimRight(env("COVERART_URL", "https://coverartarchive.org"), "/")
	cfg.MBContact = pyStrip(env("MUSICBRAINZ_CONTACT", ""))
	cfg.Port = env("PORT", "8080")
	cfg.StaticDir = env("STATIC_DIR", "static")
	cfg.CoverDir = filepath.Join(cfg.DataDir, "covers")              // covers downloaded from TMDB
	cfg.CustomCoverDir = filepath.Join(cfg.DataDir, "covers_custom") // covers uploaded by the user
	cfg.MetaDir = filepath.Join(cfg.DataDir, "meta")                 // TMDB cache
	cfg.LibFile = filepath.Join(cfg.DataDir, "library.json")         // user edits
	cfg.ResumeFile = filepath.Join(cfg.DataDir, "resume.json")
}

// writeFile replaces a file atomically (other readers never see half of it).
func writeFile(path string, value []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, value, 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONObject(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	v, err := decodeJSON(data)
	if err != nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// ---------------------------------------------------------------------------------- metadata
func readMeta(id string) map[string]any {
	if m, ok := readJSONObject(filepath.Join(cfg.MetaDir, id+".json")); ok {
		return m
	}
	return map[string]any{}
}

func storeMeta(id string, meta map[string]any) error {
	if err := os.MkdirAll(cfg.MetaDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.MetaDir, id+".json"), jsonMarshal(meta, ""), 0o644)
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------------- library
var libMu sync.Mutex

func loadLibrary() map[string]any {
	if m, ok := readJSONObject(cfg.LibFile); ok {
		return m
	}
	return map[string]any{}
}

func saveLibrary(lib map[string]any) error {
	return writeFile(cfg.LibFile, jsonMarshal(lib, " "))
}

// libRec is lib.get(fp, {}).
func libRec(lib map[string]any, fp string) map[string]any {
	if rec, ok := lib[fp].(map[string]any); ok {
		return rec
	}
	return map[string]any{}
}

// dropSeriesEdits removes the old "series" edit field: the title now names the series of a
// season disc.
func dropSeriesEdits() error {
	libMu.Lock()
	defer libMu.Unlock()
	lib := loadLibrary()
	changed := 0
	for _, v := range lib {
		if rec, ok := v.(map[string]any); ok {
			if _, has := rec["series"]; has {
				delete(rec, "series")
				changed++
			}
		}
	}
	if changed == 0 {
		return nil
	}
	for fp, v := range lib {
		if rec, ok := v.(map[string]any); ok && len(rec) == 0 {
			delete(lib, fp) // an edit that held only a series name
		}
	}
	if err := saveLibrary(lib); err != nil {
		return err
	}
	log.Printf("Removed the series field from %d edited discs", changed)
	return nil
}

// ---------------------------------------------------------------------------------- resume
var (
	resumeMu    sync.Mutex
	resumeCache map[string]any
)

func loadResumeLocked() {
	if resumeCache == nil {
		if m, ok := readJSONObject(cfg.ResumeFile); ok {
			resumeCache = m
		} else {
			resumeCache = map[string]any{}
		}
	}
}

// getResume is a copy of every saved position.
func getResume() map[string]any {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	loadResumeLocked()
	return copyMap(resumeCache)
}

func resumeEntry(fp string) map[string]any {
	e, _ := getResume()[fp].(map[string]any)
	return e
}

// setResume saves (or with nil forgets) where a disc was left.
func setResume(fp string, entry map[string]any) {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	loadResumeLocked()
	if entry == nil {
		if v, ok := resumeCache[fp]; !ok || v == nil {
			delete(resumeCache, fp)
			return
		}
		delete(resumeCache, fp)
	} else {
		resumeCache[fp] = entry
	}
	if err := writeFile(cfg.ResumeFile, jsonMarshal(resumeCache, " ")); err != nil {
		log.Printf("WARNING: Could not save resume.json: %v", err)
	}
}

func now() int64 { return time.Now().Unix() }

// ---------------------------------------------------------------------------------- state file
func writeState(value string) error { return writeFile(cfg.StateFile, []byte(value)) }

func readState() string {
	data, err := os.ReadFile(cfg.StateFile)
	if err != nil {
		return ""
	}
	return pyStrip(string(data))
}

// ---------------------------------------------------------------------------------- covers
var customExts = []string{".jpg", ".png", ".webp"}

func findCustomCover(fp string) string {
	for _, ext := range customExts {
		if p := filepath.Join(cfg.CustomCoverDir, fp+ext); isFile(p) {
			return p
		}
	}
	return ""
}

// coverVersion changes whenever the cover changes, so a browser does not show a stale one.
func coverVersion(d *Disc) int64 {
	p := findCustomCover(d.FP)
	if p == "" {
		p = d.UserCover
	}
	if p == "" {
		p = filepath.Join(cfg.CoverDir, d.ID+".jpg")
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.ModTime().Unix()
}

func sniffImage(data []byte) string {
	switch {
	case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
		return ".jpg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	}
	return ""
}

var _ = json.Number("") // json is used by the helpers in pyutil.go
