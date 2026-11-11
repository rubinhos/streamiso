// Package fsstore keeps the catalog's data in files: library.json (the user's edits), meta/<id>.json
// (what the lookups found), resume.json (where each disc was left), the covers, and the state file the
// player container watches. It implements ports.Library, Metadata, Resume, Covers and PlayerState.
package fsstore

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"streamiso/internal/compat"
	"streamiso/internal/config"
	"streamiso/internal/domain"
)

type Store struct {
	cfg config.Config

	libMu sync.Mutex

	resumeMu    sync.Mutex
	resumeCache map[string]any
}

func New(cfg config.Config) *Store { return &Store{cfg: cfg} }

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
	v, err := compat.DecodeJSON(data)
	if err != nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ---------------------------------------------------------------------------------- library.json

// Library is the ports.Library of this store.
func (s *Store) Library() *Library { return (*Library)(s) }

type Library Store

func (l *Library) Lock()   { l.libMu.Lock() }
func (l *Library) Unlock() { l.libMu.Unlock() }

func (l *Library) Load() map[string]any {
	if m, ok := readJSONObject(l.cfg.LibFile); ok {
		return m
	}
	return map[string]any{}
}

func (l *Library) Save(lib map[string]any) error {
	return writeFile(l.cfg.LibFile, compat.MarshalJSON(lib, " "))
}

// DropSeriesEdits removes the old "series" edit field: the title now names the series of a season disc.
func (s *Store) DropSeriesEdits() error {
	l := s.Library()
	l.Lock()
	defer l.Unlock()
	lib := l.Load()
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
	if err := l.Save(lib); err != nil {
		return err
	}
	log.Printf("Removed the series field from %d edited discs", changed)
	return nil
}

// ---------------------------------------------------------------------------------- meta/<id>.json

// Metadata is the ports.Metadata of this store.
func (s *Store) Metadata() *Metadata { return (*Metadata)(s) }

type Metadata Store

func (m *Metadata) path(id string) string { return filepath.Join(m.cfg.MetaDir, id+".json") }

func (m *Metadata) Read(id string) map[string]any {
	if v, ok := readJSONObject(m.path(id)); ok {
		return v
	}
	return map[string]any{}
}

func (m *Metadata) Store(id string, meta map[string]any) error {
	if err := os.MkdirAll(m.cfg.MetaDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.path(id), compat.MarshalJSON(meta, ""), 0o644)
}

func (m *Metadata) Has(id string) bool {
	_, err := os.Stat(m.path(id))
	return err == nil
}

// ---------------------------------------------------------------------------------- resume.json

// Resume is the ports.Resume of this store.
func (s *Store) Resume() *Resume { return (*Resume)(s) }

type Resume Store

func (r *Resume) loadLocked() {
	if r.resumeCache == nil {
		if m, ok := readJSONObject(r.cfg.ResumeFile); ok {
			r.resumeCache = m
		} else {
			r.resumeCache = map[string]any{}
		}
	}
}

// All is a copy of every saved position.
func (r *Resume) All() map[string]any {
	r.resumeMu.Lock()
	defer r.resumeMu.Unlock()
	r.loadLocked()
	return compat.CopyMap(r.resumeCache)
}

func (r *Resume) Get(fp string) map[string]any {
	e, _ := r.All()[fp].(map[string]any)
	return e
}

// Set saves (or with nil forgets) where a disc was left.
func (r *Resume) Set(fp string, entry map[string]any) {
	r.resumeMu.Lock()
	defer r.resumeMu.Unlock()
	r.loadLocked()
	if entry == nil {
		if v, ok := r.resumeCache[fp]; !ok || v == nil {
			delete(r.resumeCache, fp)
			return
		}
		delete(r.resumeCache, fp)
	} else {
		r.resumeCache[fp] = entry
	}
	if err := writeFile(r.cfg.ResumeFile, compat.MarshalJSON(r.resumeCache, " ")); err != nil {
		log.Printf("WARNING: Could not save resume.json: %v", err)
	}
}

// ---------------------------------------------------------------------------------- state file

// State is the ports.PlayerState of this store.
func (s *Store) State() *State { return (*State)(s) }

type State Store

func (st *State) Write(value string) error { return writeFile(st.cfg.StateFile, []byte(value)) }

func (st *State) Read() string {
	data, err := os.ReadFile(st.cfg.StateFile)
	if err != nil {
		return ""
	}
	return compat.Strip(string(data))
}

// BlurayTracks reads bluray-tracks.json, which the player's launcher writes to the player-info volume when it
// opens a Blu-ray: {"iso": path, "audio": [codes], "subtitles": [codes]}. ok is false if it is about another disc.
func (st *State) BlurayTracks(iso string) (audio, subtitles []string, ok bool) {
	data, err := os.ReadFile(filepath.Join(st.cfg.PlayerInfoDir, "bluray-tracks.json"))
	if err != nil {
		return nil, nil, false
	}
	var v struct {
		ISO       string   `json:"iso"`
		Audio     []string `json:"audio"`
		Subtitles []string `json:"subtitles"`
	}
	if json.Unmarshal(data, &v) != nil || v.ISO != iso {
		return nil, nil, false
	}
	return v.Audio, v.Subtitles, true
}

// Signal writes "<sequence> <command>" to the file "command" next to the state file. The sequence (the time
// in nanoseconds) makes each request new, so the player runs it once even if the same command repeats.
func (st *State) Signal(command string) error {
	path := filepath.Join(filepath.Dir(st.cfg.StateFile), "command")
	return writeFile(path, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)+" "+command+"\n"))
}

// ---------------------------------------------------------------------------------- covers

// Covers is the ports.Covers of this store.
func (s *Store) Covers() *Covers { return (*Covers)(s) }

type Covers Store

var customExts = []string{".jpg", ".png", ".webp"}

func (c *Covers) Custom(fp string) string {
	for _, ext := range customExts {
		if p := filepath.Join(c.cfg.CustomCoverDir, fp+ext); isFile(p) {
			return p
		}
	}
	return ""
}

func (c *Covers) SaveCustom(fp, ext string, data []byte) error {
	if err := os.MkdirAll(c.cfg.CustomCoverDir, 0o755); err != nil {
		return err
	}
	c.DeleteCustom(fp)
	return os.WriteFile(filepath.Join(c.cfg.CustomCoverDir, fp+ext), data, 0o644)
}

func (c *Covers) DeleteCustom(fp string) {
	for _, e := range customExts {
		_ = os.Remove(filepath.Join(c.cfg.CustomCoverDir, fp+e))
	}
}

func (c *Covers) downloadedPath(id string) string { return filepath.Join(c.cfg.CoverDir, id+".jpg") }

func (c *Covers) Downloaded(id string) string {
	if p := c.downloadedPath(id); isFile(p) {
		return p
	}
	return ""
}

func (c *Covers) SaveDownloaded(id string, data []byte) error {
	if err := os.MkdirAll(c.cfg.CoverDir, 0o755); err != nil {
		return err
	}
	tmp := c.downloadedPath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.downloadedPath(id))
}

// Version changes whenever the cover changes, so a browser does not show a stale one.
func (c *Covers) Version(d *domain.Disc) int64 {
	p := c.Custom(d.FP)
	if p == "" {
		p = d.UserCover
	}
	if p == "" {
		p = c.downloadedPath(d.ID)
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.ModTime().Unix()
}
