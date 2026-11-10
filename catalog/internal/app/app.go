// Package app holds the catalog's use cases: the catalog of discs (Catalog), the user's library and its
// edits (Library), the lookups on TMDB and MusicBrainz (Metadata) and playback (Playback). They work through
// the interfaces in ports only, so they know nothing about files, HTTP or VLC.
package app

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"streamiso/internal/domain"
	"streamiso/internal/ports"
)

// Failure is a use case that could not be done, with the HTTP status and message the API answers.
type Failure struct {
	Status int
	Msg    string
}

func (f *Failure) Error() string { return f.Msg }

func fail(status int, msg string) error { return &Failure{Status: status, Msg: msg} }

// ErrText describes an error for the user, naming the status of an outside service's error answer.
func ErrText(err error) string {
	var h *ports.HTTPError
	if errors.As(err, &h) {
		return fmt.Sprintf("HTTP Error %d: %s", h.Code, http.StatusText(h.Code))
	}
	return err.Error()
}

// now is the time stored in meta and resume entries, in seconds.
func now() int64 { return time.Now().Unix() }

// libRec is the user's edits of one disc (an empty map when there are none).
func libRec(lib map[string]any, fp string) map[string]any {
	if rec, ok := lib[fp].(map[string]any); ok {
		return rec
	}
	return map[string]any{}
}

// ---------------------------------------------------------------------------------- catalog

const rescanTTL = 30 * time.Second // between automatic rescans

// Catalog is the last scan of the media folders, done again when it is older than 30 seconds.
type Catalog struct {
	scanner ports.MediaScanner

	mu        sync.Mutex
	cat       *domain.Catalog
	scannedAt time.Time

	// OnScan runs in the background after each scan (the lookups of what is new).
	OnScan func()
}

func NewCatalog(scanner ports.MediaScanner) *Catalog {
	return &Catalog{scanner: scanner, cat: domain.NewCatalog()}
}

// Get rescans the folders when the last scan is older than rescanTTL, or when forced.
func (c *Catalog) Get(force bool) *domain.Catalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	if force || time.Since(c.scannedAt) > rescanTTL {
		c.cat = c.scanner.Scan()
		c.scannedAt = time.Now()
		if c.OnScan != nil {
			go c.OnScan()
		}
	}
	return c.cat
}

// Disc finds a disc by id (nil if there is none).
func (c *Catalog) Disc(id string) *domain.Disc { return c.Get(false).Get(id) }

// Current is the catalog as last scanned, without scanning.
func (c *Catalog) Current() []*domain.Disc {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cat.Values()
}
