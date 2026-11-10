// Package ports names what the application needs from the outside world. The services in app use only
// these interfaces; the adapters implement them, and cmd/catalog plugs one into the other.
package ports

import (
	"fmt"
	"time"

	"streamiso/internal/domain"
)

// MediaScanner reads the media folders: DVD images and audio CD images.
type MediaScanner interface {
	Scan() *domain.Catalog
}

// Library holds the user's edits (library.json), keyed by the disc's fingerprint. Lock around a
// load-change-save, so two edits at once do not undo each other.
type Library interface {
	Load() map[string]any
	Save(lib map[string]any) error
	Lock()
	Unlock()
}

// Metadata holds what a lookup found for each disc (TMDB or MusicBrainz), by disc id.
type Metadata interface {
	Read(id string) map[string]any
	Store(id string, meta map[string]any) error
	Has(id string) bool
}

// Resume holds where each disc was left, by fingerprint.
type Resume interface {
	All() map[string]any
	Get(fp string) map[string]any
	Set(fp string, entry map[string]any) // nil forgets it
}

// PlayerState is what the player opens: an image path, or "" for nothing. The player container watches it.
type PlayerState interface {
	Read() string
	Write(state string) error
}

// Covers holds the covers: the ones the user uploaded (by fingerprint) and the downloaded ones (by id).
type Covers interface {
	Custom(fp string) string // the file, or ""
	SaveCustom(fp, ext string, data []byte) error
	DeleteCustom(fp string)
	Downloaded(id string) string // the file, or ""
	SaveDownloaded(id string, data []byte) error
	Version(d *domain.Disc) int64 // changes whenever the disc's cover does
}

// Player drives the program that plays the disc (VLC). Its errors are *Unavailable when it does not answer,
// and *domain.ValueError for a bad command.
type Player interface {
	Status() (map[string]any, error)
	Playback(withLength bool) (map[string]any, error)
	Time() (any, error)
	Length() (any, error)
	SetTitle(n int64) error
	Nav(direction string) error
	Pause() error
	SeekTo(seconds int64) error
	SetVolume(percent int64) error
	NextChapter() error
	PrevChapter() error
	SetAudio(id int64) error
	SetSubtitle(id int64) error
	DiscMenu() error
	PartMenu() error
}

// Unavailable means a service did not answer (the player is starting, for example).
type Unavailable struct{ Msg string }

func (e *Unavailable) Error() string { return e.Msg }

// MovieDB is TMDB's API: a GET of one of its paths, and its poster images.
type MovieDB interface {
	Get(path string, params ...[2]string) (map[string]any, error)
	Image(path string) ([]byte, error)
}

// AlbumHit is the album a music lookup found.
type AlbumHit struct {
	MBID, Title, Artist, Year string
	Genres                    []string
}

// MusicDB finds audio CDs and their covers (MusicBrainz and the Cover Art Archive).
type MusicDB interface {
	LookupAlbum(title, artist string) (*AlbumHit, error)
	FetchCover(mbid string) ([]byte, error) // nil when there is none
	Pause()                                 // the wait the service asks for between requests
}

// PlainVideo is the HLS stream for TVs whose WebRTC cannot connect (see docs/API.md).
type PlainVideo interface {
	Available() bool
	Start() error
	WaitReady(timeout time.Duration) bool
	Stop()
	File(name string) string // the playlist or a segment, or ""; counts as watching
}

// HTTPError is an answer with an error status from an outside service.
type HTTPError struct {
	Code int
	URL  string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP Error %d", e.Code) }
