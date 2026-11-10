package main

// Minimal client for VLC's remote-control (rc) interface.
//
// VLC runs inside the Neko container with --extraintf=rc --rc-host=0.0.0.0:4212. The protocol is
// line based: after each command VLC replies and prints a "> " prompt. Every call opens a
// short-lived connection, so a VLC restart (a new disc) never leaves a stale socket behind.

import (
	"bytes"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const volume100 = 256 // rc volume scale: 256 is 100 % (maximum 512)

// VlcError means VLC is not reachable or did not answer.
type VlcError struct{ msg string }

func (e *VlcError) Error() string { return e.msg }

// ValueError is a bad value in a command (answered with 400).
type ValueError struct{ msg string }

func (e *ValueError) Error() string { return e.msg }

type VlcRc struct {
	host    string
	port    int
	timeout time.Duration
	mu      sync.Mutex
}

var vlc *VlcRc

func readUntilPrompt(conn net.Conn, timeout time.Duration) (string, error) {
	var buf []byte
	chunk := make([]byte, 4096)
	for !bytes.HasSuffix(buf, []byte("> ")) {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			continue
		}
		if err != nil {
			return "", &VlcError{err.Error()}
		}
		return "", &VlcError{"VLC closed the connection"}
	}
	return strings.ToValidUTF8(string(buf[:len(buf)-2]), "�"), nil
}

// run sends the commands in order on one connection and returns one reply per command.
func (v *VlcRc) run(commands ...string) ([]string, error) {
	for _, c := range commands {
		if strings.ContainsAny(c, "\r\n") {
			return nil, &ValueError{"newline in command"}
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(v.host, strconv.Itoa(v.port)), v.timeout)
	if err != nil {
		return nil, &VlcError{err.Error()}
	}
	defer conn.Close()
	if _, err := readUntilPrompt(conn, v.timeout); err != nil { // banner
		return nil, err
	}
	replies := make([]string, 0, len(commands))
	for _, c := range commands {
		_ = conn.SetWriteDeadline(time.Now().Add(v.timeout))
		if _, err := conn.Write([]byte(c + "\n")); err != nil {
			return nil, &VlcError{err.Error()}
		}
		r, err := readUntilPrompt(conn, v.timeout)
		if err != nil {
			return nil, err
		}
		replies = append(replies, r)
	}
	return replies, nil
}

var (
	intRE    = regexp.MustCompile(`-?\d+`)
	trackRE  = regexp.MustCompile(`^\|\s*(-?\d+)\s*-\s*(.*?)(\s+\*)?\s*$`)
	stateRE  = regexp.MustCompile(`\(\s*state\s+(\w+)\s*\)`)
	volumeRE = regexp.MustCompile(`audio volume:\s*(\d+)`)
)

// rcInt is the first number in a reply, or nil.
func rcInt(text string) any {
	m := intRE.FindString(text)
	if m == "" {
		return nil
	}
	n, err := strconv.ParseInt(m, 10, 64)
	if err != nil {
		return nil
	}
	return n
}

func rcTracks(text string) []map[string]any {
	out := []map[string]any{}
	for _, line := range splitLines(text) {
		m := trackRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id, _ := strconv.Atoi(m[1])
		out = append(out, map[string]any{"id": id, "name": m[2], "selected": m[3] != ""})
	}
	return out
}

func rcStateVolume(reply string) (string, any) {
	state := "unknown"
	if m := stateRE.FindStringSubmatch(reply); m != nil {
		state = m[1]
	}
	var volume any
	if m := volumeRE.FindStringSubmatch(reply); m != nil {
		raw, _ := strconv.ParseFloat(m[1], 64)
		vol := pyRound(raw * 100 / volume100)
		if vol > 100 {
			vol = 100
		}
		volume = vol
	}
	return state, volume
}

func (v *VlcRc) status() (map[string]any, error) {
	r, err := v.run("status", "get_time", "get_length", "atrack", "strack", "chapter", "title")
	if err != nil {
		return nil, err
	}
	state, volume := rcStateVolume(r[0])
	audio := []map[string]any{}
	for _, t := range rcTracks(r[3]) {
		if t["id"].(int) >= 0 { // -1 is VLC's "Disable" entry; keep it for subtitles, hide it for audio
			audio = append(audio, t)
		}
	}
	return map[string]any{
		"state":     state,
		"time":      rcInt(r[1]),
		"length":    rcInt(r[2]),
		"volume":    volume,
		"chapter":   rcInt(r[5]),
		"title":     rcInt(r[6]),
		"audio":     audio,
		"subtitles": rcTracks(r[4]),
	}, nil
}

// playback gives the state, volume and position only (for audio CDs, which have no tracks to list).
func (v *VlcRc) playback(withLength bool) (map[string]any, error) {
	cmds := []string{"status", "get_time"}
	if withLength {
		cmds = append(cmds, "get_length")
	}
	r, err := v.run(cmds...)
	if err != nil {
		return nil, err
	}
	state, volume := rcStateVolume(r[0])
	var length any
	if withLength {
		length = rcInt(r[2])
	}
	return map[string]any{"state": state, "time": rcInt(r[1]), "length": length, "volume": volume}, nil
}

func (v *VlcRc) one(cmd string) (any, error) {
	r, err := v.run(cmd)
	if err != nil {
		return nil, err
	}
	return rcInt(r[0]), nil
}

func (v *VlcRc) getTime() (any, error)   { return v.one("get_time") }
func (v *VlcRc) getLength() (any, error) { return v.one("get_length") }

func (v *VlcRc) do(cmd string) error {
	_, err := v.run(cmd)
	return err
}

func (v *VlcRc) setTitle(n int64) error { return v.do(fmt.Sprintf("title %d", n)) }

// nav moves in a DVD menu or presses the highlighted button ("activate").
func (v *VlcRc) nav(direction string) error {
	switch direction {
	case "up", "down", "left", "right", "activate":
		return v.do("key key-nav-" + direction)
	}
	return &ValueError{"unknown direction"}
}

func (v *VlcRc) pause() error { return v.do("pause") }

func (v *VlcRc) seek(seconds int64) error {
	if seconds < 0 {
		seconds = 0
	}
	return v.do(fmt.Sprintf("seek %d", seconds))
}

func (v *VlcRc) setVolume(percent int64) error {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return v.do(fmt.Sprintf("volume %d", pyRound(float64(percent)*volume100/100)))
}

func (v *VlcRc) nextChapter() error         { return v.do("chapter_n") }
func (v *VlcRc) prevChapter() error         { return v.do("chapter_p") }
func (v *VlcRc) setAudio(id int64) error    { return v.do(fmt.Sprintf("atrack %d", id)) }
func (v *VlcRc) setSubtitle(id int64) error { return v.do(fmt.Sprintf("strack %d", id)) }

// discMenu opens the disc's main menu (VLC's menu key calls the Title menu, else the Root menu).
func (v *VlcRc) discMenu() error { return v.do("key key-disc-menu") }

// partMenu opens the Root menu of the part of the disc that is playing, as the Menu key of a DVD
// remote. Title 0 is how VLC's DVD module calls it.
func (v *VlcRc) partMenu() error { return v.do("title 0") }

// timeOr0 is (value or 0) for a position read from VLC.
func timeOr0(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return 0
}
