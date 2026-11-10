// Package vlc is a minimal client for VLC's remote-control (rc) interface. It implements ports.Player.
//
// VLC runs inside the Neko container with --extraintf=rc --rc-host=0.0.0.0:4212. The protocol is
// line based: after each command VLC replies and prints a "> " prompt. Every call opens a
// short-lived connection, so a VLC restart (a new disc) never leaves a stale socket behind.
package vlc

import (
	"bytes"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"streamiso/internal/compat"
	"streamiso/internal/domain"
	"streamiso/internal/ports"
	"strings"
	"sync"
	"time"
)

func New(host string, port int, timeout time.Duration) *Client {
	return &Client{host: host, port: port, timeout: timeout}
}

const volume100 = 256 // rc volume scale: 256 is 100 % (maximum 512)

type Client struct {
	host    string
	port    int
	timeout time.Duration
	mu      sync.Mutex
}

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
			return "", &ports.Unavailable{Msg: err.Error()}
		}
		return "", &ports.Unavailable{Msg: "VLC closed the connection"}
	}
	return strings.ToValidUTF8(string(buf[:len(buf)-2]), "�"), nil
}

// run sends the commands in order on one connection and returns one reply per command.
func (v *Client) run(commands ...string) ([]string, error) {
	for _, c := range commands {
		if strings.ContainsAny(c, "\r\n") {
			return nil, &domain.ValueError{Msg: "newline in command"}
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(v.host, strconv.Itoa(v.port)), v.timeout)
	if err != nil {
		return nil, &ports.Unavailable{Msg: err.Error()}
	}
	defer conn.Close()
	if _, err := readUntilPrompt(conn, v.timeout); err != nil { // banner
		return nil, err
	}
	replies := make([]string, 0, len(commands))
	for _, c := range commands {
		_ = conn.SetWriteDeadline(time.Now().Add(v.timeout))
		if _, err := conn.Write([]byte(c + "\n")); err != nil {
			return nil, &ports.Unavailable{Msg: err.Error()}
		}
		r, err := readUntilPrompt(conn, v.timeout)
		if err != nil {
			return nil, err
		}
		replies = append(replies, r)
	}
	return replies, nil
}

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
	for _, line := range compat.SplitLines(text) {
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
		vol := compat.Round(raw * 100 / volume100)
		if vol > 100 {
			vol = 100
		}
		volume = vol
	}
	return state, volume
}

func (v *Client) Status() (map[string]any, error) {
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

// Playback gives the state, volume and position only (for audio CDs, which have no tracks to list).
func (v *Client) Playback(withLength bool) (map[string]any, error) {
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

func (v *Client) one(cmd string) (any, error) {
	r, err := v.run(cmd)
	if err != nil {
		return nil, err
	}
	return rcInt(r[0]), nil
}

func (v *Client) Time() (any, error) { return v.one("get_time") }

func (v *Client) Length() (any, error) { return v.one("get_length") }

func (v *Client) do(cmd string) error {
	_, err := v.run(cmd)
	return err
}

func (v *Client) SetTitle(n int64) error { return v.do(fmt.Sprintf("title %d", n)) }

// Nav moves in a DVD menu or presses the highlighted button ("activate").
func (v *Client) Nav(direction string) error {
	switch direction {
	case "up", "down", "left", "right", "activate":
		return v.do("key key-nav-" + direction)
	}
	return &domain.ValueError{Msg: "unknown direction"}
}

func (v *Client) Pause() error { return v.do("pause") }

func (v *Client) SeekTo(seconds int64) error {
	if seconds < 0 {
		seconds = 0
	}
	return v.do(fmt.Sprintf("seek %d", seconds))
}

func (v *Client) SetVolume(percent int64) error {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return v.do(fmt.Sprintf("volume %d", compat.Round(float64(percent)*volume100/100)))
}

func (v *Client) NextChapter() error { return v.do("chapter_n") }

func (v *Client) PrevChapter() error { return v.do("chapter_p") }

func (v *Client) SetAudio(id int64) error { return v.do(fmt.Sprintf("atrack %d", id)) }

func (v *Client) SetSubtitle(id int64) error { return v.do(fmt.Sprintf("strack %d", id)) }

// DiscMenu opens the disc's main menu (VLC's menu key calls the Title menu, else the Root menu).
func (v *Client) DiscMenu() error { return v.do("key key-disc-menu") }

// PartMenu opens the Root menu of the part of the disc that is playing, as the Menu key of a DVD
// remote. Title 0 is how VLC's DVD module calls it.
func (v *Client) PartMenu() error { return v.do("title 0") }

var (
	intRE    = regexp.MustCompile(`-?\d+`)
	trackRE  = regexp.MustCompile(`^\|\s*(-?\d+)\s*-\s*(.*?)(\s+\*)?\s*$`)
	stateRE  = regexp.MustCompile(`\(\s*state\s+(\w+)\s*\)`)
	volumeRE = regexp.MustCompile(`audio volume:\s*(\d+)`)
)
