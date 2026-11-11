package app

// Playback: opening and closing discs, the player's state and commands, where a disc was left, and the
// plain video for TVs whose WebRTC cannot connect.

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"streamiso/internal/ports"
)

type resumeAt struct{ Title, Time int64 }

type Playback struct {
	player  ports.Player
	state   ports.PlayerState
	resume  ports.Resume
	covers  ports.Covers
	video   ports.PlainVideo
	catalog *Catalog
	library *Library

	// The screen VLC draws on: a Blu-ray gets its own size (BLURAY_RESOLUTION), the other discs the usual one.
	screen           ports.Screen
	discSize, bdSize [2]int

	// pending is what the player still has to do for the disc that was just opened. VLC opens
	// paused, so the first seconds are not lost before the viewer's audio and video connect.
	pendMu  sync.Mutex
	pending struct {
		set    bool
		State  string
		Wait   bool
		Resume *resumeAt
	}
}

func NewPlayback(player ports.Player, state ports.PlayerState, resume ports.Resume, covers ports.Covers,
	video ports.PlainVideo, catalog *Catalog, library *Library) *Playback {
	return &Playback{player: player, state: state, resume: resume, covers: covers, video: video, catalog: catalog, library: library}
}

// UseScreen lets Play give a Blu-ray its own screen size (bluray, e.g. 1920x1080) and the other discs the usual
// one (disc). With no bluray size, the screen is never changed.
func (p *Playback) UseScreen(screen ports.Screen, disc, bluray [2]int) {
	p.screen, p.discSize, p.bdSize = screen, disc, bluray
}

// fitScreen sets the screen size for the disc about to open. A Blu-ray is 1920x1080, so a bigger screen adds
// detail; a DVD is at most 720x576, where it would only cost more. Neko rebuilds its encoding at the new size,
// and the bit rate follows it (see NEKO_CAPTURE_VIDEO_PIPELINES in docker-compose.yml).
func (p *Playback) fitScreen(d *domain.Disc) {
	if p.screen == nil || p.bdSize[0] == 0 || p.discSize[0] == 0 {
		return
	}
	size := p.discSize
	if d.Format == "bluray" {
		size = p.bdSize
	}
	if err := p.screen.SetScreenSize(size[0], size[1]); err != nil {
		log.Printf("WARNING: Screen size %dx%d: %v", size[0], size[1], err)
	}
}

func (p *Playback) pendingFor(d *domain.Disc) (wait bool, res *resumeAt, ok bool) {
	p.pendMu.Lock()
	defer p.pendMu.Unlock()
	if d == nil || !p.pending.set || p.pending.State != d.State {
		return false, nil, false
	}
	return p.pending.Wait, p.pending.Resume, true
}

// rememberPosition saves the playback position (from the status poll, about once per second).
func (p *Playback) rememberPosition(d *domain.Disc, pos, length, title any) {
	if wait, res, ok := p.pendingFor(d); ok && (wait || res != nil) {
		return // still starting up: the position says nothing yet
	}
	if d.Format == "bluray" {
		// The disc's own (BD-J) program picks what plays, and VLC reports every playlist as title 1, so a
		// saved time could not be found again: reopening would jump inside the studio logo or a trailer.
		return
	}
	at, ok := compat.ToFloat(pos)
	if !ok {
		return
	}
	isCD := d.Kind == "cd"
	minPos := 30.0
	if isCD {
		minPos = 10
	}
	if at < minPos {
		return
	}
	l, hasLen := compat.ToFloat(length)
	if !hasLen {
		l = 0
	}
	if !isCD && l < 300 {
		return // a menu or a short extra, nothing worth resuming
	}
	if l != 0 {
		margin := 5.0
		if !isCD {
			margin = math.Max(60, l*0.03)
		}
		if at >= l-margin {
			p.resume.Set(d.FP, nil) // reached the end: next time starts over
			return
		}
	}
	if last := p.resume.Get(d.FP); len(last) > 0 {
		t, ok := compat.ToFloat(compat.GetOr(last, "time", int64(-99)))
		if ok && math.Abs(t-at) < 5 {
			return
		}
	}
	var lenOut any
	if l != 0 {
		lenOut = int64(l)
	}
	var titleOut int64
	if t, ok := compat.ToFloat(title); ok && t != 0 {
		titleOut = int64(t)
	}
	p.resume.Set(d.FP, map[string]any{"time": int64(at), "length": lenOut, "title": titleOut, "updated": now()})
}

// IsOpen tells whether the player was told to open something.
func (p *Playback) IsOpen() bool { return p.state.Read() != "" }

// Playing is the disc that is open, or nil.
func (p *Playback) Playing() *domain.Disc {
	state := p.state.Read()
	if state == "" {
		return nil
	}
	for _, d := range p.catalog.Get(false).Values() {
		if d.State == state {
			return d
		}
	}
	return nil
}

func cdTracksView(d *domain.Disc) []any {
	out := make([]any, 0, len(d.Tracks))
	for _, t := range d.Tracks {
		var length any
		if t.Length != nil {
			length = compat.Round2(*t.Length)
		}
		out = append(out, map[string]any{"n": t.N, "title": t.Title, "artist": t.Artist, "start": t.Start, "length": length})
	}
	return out
}

// timeOr0 is (value or 0) for a position read from the player.
func timeOr0(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return 0
}

// cdStatus is the playback state of an audio CD: time and length relative to the current track.
func (p *Playback) cdStatus(d *domain.Disc) (map[string]any, error) {
	st, err := p.player.Playback(!d.Raw)
	if err != nil {
		return nil, err
	}
	spans := d.Tracks
	if l, ok := compat.ToFloat(st["length"]); !d.Raw && ok && l != 0 { // a single audio file: the last track ends with it
		spans = domain.TrackSpans(domain.SpansTracks(spans), &l)
	}
	pos := timeOr0(st["time"])
	idx := domain.Locate(spans, pos)
	cur := spans[idx]
	start := int64(cur.Start)
	var length any
	if cur.Length != nil {
		length = compat.Round(*cur.Length)
	}
	t := pos - start
	if t < 0 {
		t = 0
	}
	return map[string]any{"state": st["state"], "kind": "cd", "volume": st["volume"], "track": idx,
		"tracks": len(spans), "time": t, "length": length, "abs": pos}, nil
}

// intField reads a number from the payload, clamped to [lo, hi].
func intField(payload map[string]any, key string, lo, hi int64) (int64, error) {
	v := payload[key]
	if _, isBool := v.(bool); isBool {
		return 0, &domain.ValueError{Msg: fmt.Sprintf("'%s' must be a number", key)}
	}
	f, ok := compat.ToFloat(v)
	if !ok {
		return 0, &domain.ValueError{Msg: fmt.Sprintf("'%s' must be a number", key)}
	}
	f = math.Max(float64(lo), math.Min(float64(hi), math.Trunc(f)))
	return int64(f), nil
}

// cdCommand handles the commands that differ for audio CDs; false if the action is not one of them.
func (p *Playback) cdCommand(d *domain.Disc, action any, payload map[string]any) (bool, error) {
	spans := d.Tracks
	a, _ := action.(string)
	switch a {
	case "seek": // seconds from the start of the current track
		t, err := p.player.Time()
		if err != nil {
			return true, err
		}
		cur := spans[domain.Locate(spans, timeOr0(t))]
		v, err := intField(payload, "value", 0, 86400)
		if err != nil {
			return true, err
		}
		return true, p.player.SeekTo(int64(cur.Start) + v)
	case "track_next", "chapter_next":
		t, err := p.player.Time()
		if err != nil {
			return true, err
		}
		idx := domain.Locate(spans, timeOr0(t))
		if idx+1 >= len(spans) {
			return true, &domain.ValueError{Msg: "This is the last track"}
		}
		return true, p.player.SeekTo(int64(spans[idx+1].Start))
	case "track_prev", "chapter_prev":
		t, err := p.player.Time()
		if err != nil {
			return true, err
		}
		pos := timeOr0(t)
		idx := domain.Locate(spans, pos)
		// like a CD player: restart the track, or go to the previous one if it just began
		if pos-int64(spans[idx].Start) > 3 || idx == 0 {
			return true, p.player.SeekTo(int64(spans[idx].Start))
		}
		return true, p.player.SeekTo(int64(spans[idx-1].Start))
	case "track": // play track number (index) N
		idx, err := intField(payload, "value", 0, int64(len(spans)-1))
		if err != nil {
			return true, err
		}
		return true, p.player.SeekTo(int64(spans[idx].Start))
	}
	return false, nil
}

// applyResume jumps to the saved position (once). The disc is still paused meanwhile.
func (p *Playback) applyResume(d *domain.Disc) error {
	_, res, ok := p.pendingFor(d)
	if !ok || res == nil {
		return nil
	}
	p.pendMu.Lock()
	p.pending.Resume = nil
	p.pendMu.Unlock()
	if d.Kind == "cd" {
		return p.player.SeekTo(res.Time)
	}
	for i := 0; i < 25; i++ { // wait until the player has opened the disc
		l, err := p.player.Length()
		if err != nil {
			return err
		}
		if timeOr0(l) > 0 {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if res.Title > 0 {
		if err := p.player.SetTitle(res.Title); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
	return p.player.SeekTo(res.Time)
}

// startPlayback lets a disc that was opened paused start playing (once).
func (p *Playback) startPlayback(d *domain.Disc) error {
	wait, _, ok := p.pendingFor(d)
	waiting := ok && wait
	st, err := p.player.Playback(false)
	if err != nil {
		return err
	}
	if st["state"] == "paused" && (waiting || timeOr0(st["time"]) <= 2) {
		if err := p.player.Pause(); err != nil {
			return err
		}
	}
	if waiting {
		p.pendMu.Lock()
		p.pending.Wait = false
		p.pendMu.Unlock()
	}
	return nil
}

// ---------------------------------------------------------------------------------- the use cases

// Play opens a disc, from where it was left if resume is asked (else it starts over).
func (p *Playback) Play(d *domain.Disc, resume bool) (map[string]any, error) {
	entry := p.resume.Get(d.FP)
	var res *resumeAt
	if d.Format == "bluray" {
		resume = false // see rememberPosition
	}
	if resume && len(entry) > 0 {
		t := compat.Int(compat.GetOr(entry, "time", int64(0))) - 5
		if t < 0 {
			t = 0
		}
		res = &resumeAt{Title: compat.Int(compat.FirstTruthy(int64(0), entry["title"])), Time: t}
	} else if len(entry) > 0 {
		p.resume.Set(d.FP, nil) // started over on purpose
	}
	p.pendMu.Lock()
	p.pending.set, p.pending.State, p.pending.Wait, p.pending.Resume = true, d.State, true, res
	p.pendMu.Unlock()
	p.fitScreen(d) // before VLC starts, so it opens at the right size
	if err := p.state.Write(d.State); err != nil {
		return nil, err
	}
	return map[string]any{"player_url": "/player", "title": p.library.ShownTitle(d), "kind": d.Kind, "format": formatView(d)}, nil
}

// Stop closes the disc, and the plain video if it runs.
func (p *Playback) Stop() error {
	if err := p.state.Write(""); err != nil {
		return err
	}
	go p.video.Stop()
	return nil
}

// Info is what is open: title, kind, the position it resumes from and, for a CD, its tracks and cover.
func (p *Playback) Info() map[string]any {
	d := p.Playing()
	out := map[string]any{"title": nil, "id": nil, "kind": "dvd", "format": nil}
	if d != nil {
		out["title"] = p.library.ShownTitle(d)
		out["id"] = d.ID
		out["kind"] = d.Kind
		out["format"] = formatView(d)
		_, res, _ := p.pendingFor(d)
		out["resume"] = res != nil
		out["resume_at"] = nil
		if res != nil {
			out["resume_at"] = res.Time
		}
		if d.Kind == "cd" {
			out["artist"] = d.Artist
			out["tracks"] = cdTracksView(d)
			out["cover"] = fmt.Sprintf("/api/cover/%s?v=%d", d.ID, p.covers.Version(d))
		}
	}
	return out
}

// nameBlurayTracks gives a Blu-ray's tracks their real languages. With menus, VLC names every track after
// the disc's first clip (often a studio logo in English only) and keeps those names for the film, although
// choosing a track does pick the right one. The player reads the languages of the disc's main title when it
// opens it; they are applied only when their number matches the tracks listed.
func (p *Playback) nameBlurayTracks(d *domain.Disc, st map[string]any) {
	audio, subtitles, ok := p.state.BlurayTracks(d.Path)
	if !ok {
		return
	}
	if tracks, isList := st["audio"].([]map[string]any); isList {
		st["audio"] = domain.RelabelTracks(tracks, audio)
	}
	if tracks, isList := st["subtitles"].([]map[string]any); isList {
		st["subtitles"] = domain.RelabelTracks(tracks, subtitles)
	}
}

// Status is the player's state; it also saves where the disc is, for resuming.
func (p *Playback) Status() (map[string]any, error) {
	if p.state.Read() == "" {
		return map[string]any{"state": "idle"}, nil
	}
	d := p.Playing()
	var st map[string]any
	var err error
	if d != nil && d.Kind == "cd" {
		st, err = p.cdStatus(d)
		if err == nil && (st["state"] == "playing" || st["state"] == "paused") {
			var dur any
			if d.Duration != nil && *d.Duration != 0 {
				dur = int64(*d.Duration)
			}
			p.rememberPosition(d, st["abs"], dur, nil)
		}
	} else {
		st, err = p.player.Status()
		if err == nil && d != nil && (st["state"] == "playing" || st["state"] == "paused") {
			p.rememberPosition(d, st["time"], st["length"], st["title"])
		}
		if err == nil && d != nil && d.Format == "bluray" {
			p.nameBlurayTracks(d, st)
		}
	}
	if err != nil {
		var u *ports.Unavailable
		if errors.As(err, &u) { // the player is still starting (or restarting for a new disc)
			return map[string]any{"state": "offline"}, nil
		}
		return nil, err
	}
	return st, nil
}

// Command runs a player command ({"action": ..., "value": ...}, see docs/API.md).
func (p *Playback) Command(payload map[string]any) error {
	if p.state.Read() == "" {
		return fail(409, "Nothing is playing")
	}
	if payload == nil {
		return fail(400, "Invalid JSON")
	}
	action := payload["action"]
	d := p.Playing()
	err := func() error {
		switch action {
		case "resume_apply":
			if d != nil {
				return p.applyResume(d)
			}
			return nil
		case "start":
			return p.startPlayback(d)
		case "nav":
			return p.player.Nav(compat.Str(compat.GetOr(payload, "value", "")))
		}
		if d != nil && d.Kind == "cd" {
			if handled, err := p.cdCommand(d, action, payload); handled || err != nil {
				return err
			}
		}
		number := func(lo, hi int64, set func(int64) error) error {
			v, err := intField(payload, "value", lo, hi)
			if err != nil {
				return err
			}
			return set(v)
		}
		switch action {
		case "toggle":
			return p.player.Pause()
		case "seek":
			return number(0, 86400, p.player.SeekTo)
		case "chapter_next":
			return p.player.NextChapter()
		case "chapter_prev":
			return p.player.PrevChapter()
		case "volume":
			return number(0, 100, p.player.SetVolume)
		case "audio":
			return number(0, 1000000, p.player.SetAudio)
		case "subtitle":
			return number(-1, 1000000, p.player.SetSubtitle)
		case "menu", "return":
			if d != nil && d.Format == "bluray" {
				// Discs refuse the top menu during the film (VLC then jumps elsewhere in it), but most open
				// their pop-up menu, the remote's Pop-up key, which usually leads back to the main menu too.
				// VLC's remote-control port has no command for it, so it goes through the state folder.
				if action == "return" {
					return &domain.ValueError{Msg: "Use the menu button: on a Blu-ray it opens the disc's pop-up menu"}
				}
				return p.state.Signal("popup")
			}
			if action == "menu" {
				return p.player.DiscMenu()
			}
			return p.player.PartMenu()
		}
		return fail(400, "Unknown action")
	}()
	var ve *domain.ValueError
	var u *ports.Unavailable
	switch {
	case errors.As(err, &ve):
		return fail(400, ve.Msg)
	case errors.As(err, &u):
		return fail(503, "Player not ready")
	}
	return err
}

// StartPlainVideo starts the plain video (HLS) if needed and gives its URL once it can be played.
func (p *Playback) StartPlainVideo() (string, error) {
	if !p.video.Available() {
		return "", fail(501, "Plain video is not set up on this server: see HLS in the README")
	}
	if err := p.video.Start(); err != nil {
		log.Printf("WARNING: Plain video: %v", err)
		return "", fail(502, err.Error())
	}
	if !p.video.WaitReady(10 * time.Second) {
		return "", fail(504, "The plain video did not start")
	}
	return "/api/hls/index.m3u8", nil
}

// PlainVideoFile is a file of the plain video, or "" if there is no such file.
func (p *Playback) PlainVideoFile(name string) string { return p.video.File(name) }
