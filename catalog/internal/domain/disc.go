// Package domain holds the catalog's rules, with no I/O: what a disc is, how titles, seasons and genres
// are read, which edits are valid, and how a CD's tracks are laid out. Everything else depends on it, and
// it depends on nothing but compat.
package domain

import "sync"

// Disc is one entry of the catalog. Year and UserCover are "" when unknown.
type Disc struct {
	ID         string
	FP         string // fingerprint of the image: edits and resume positions are kept under it
	Kind       string // "dvd" or "cd"; a Blu-ray is a "dvd" too, since it plays the same way (menus, VLC)
	Format     string // "bluray" for a Blu-ray image, "" otherwise
	Path       string
	State      string // what the player is told to open (see ports.PlayerState)
	Rel        string
	Title      string
	Artist     string
	Year       string
	Label      string
	Size       int64
	Added      int64
	Raw        bool
	Duration   *float64
	Tracks     []Span
	GenresAuto []string
	UserCover  string

	mu         sync.Mutex
	coverFound *bool // set by a CD lookup, read once by the lookup's answer
}

// SetCoverFound records whether a CD lookup found a cover.
func (d *Disc) SetCoverFound(found bool) {
	d.mu.Lock()
	d.coverFound = &found
	d.mu.Unlock()
}

// TakeCoverFound gives what the last CD lookup said about the cover, once (ok is false if nothing).
func (d *Disc) TakeCoverFound() (found, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.coverFound == nil {
		return false, false
	}
	found, d.coverFound = *d.coverFound, nil
	return found, true
}

// Catalog keeps the discs in the order they were found.
type Catalog struct {
	order []string
	byID  map[string]*Disc
}

func NewCatalog() *Catalog { return &Catalog{byID: map[string]*Disc{}} }

func (c *Catalog) Put(d *Disc) {
	if _, ok := c.byID[d.ID]; !ok {
		c.order = append(c.order, d.ID)
	}
	c.byID[d.ID] = d
}

func (c *Catalog) Get(id string) *Disc { return c.byID[id] }

func (c *Catalog) Len() int { return len(c.order) }

func (c *Catalog) Values() []*Disc {
	out := make([]*Disc, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.byID[id])
	}
	return out
}
