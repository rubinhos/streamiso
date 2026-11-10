// Package neko runs the plain video (HLS) for TVs whose WebRTC cannot connect to Neko. It implements
// ports.PlainVideo.
//
// Some TVs have WebRTC too old for Neko: webOS 3 (Chrome 38) only speaks DTLS 1.0, and Neko needs DTLS 1.2.
// They can still play an ordinary video stream. docker-compose.yml sets Neko's broadcast to write the same
// screen and sound it streams over WebRTC as HLS segments, in a folder it shares with the catalog. This
// starts that broadcast through Neko's API when such a TV asks, serves its files, and stops it once nobody
// has fetched them for a while, so the extra encoding only runs while someone watches this way.
package neko

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Only what Neko's hlssink writes (see NEKO_CAPTURE_BROADCAST_PIPELINE in docker-compose.yml).
var hlsFileName = regexp.MustCompile(`^(index\.m3u8|seg\d{5}\.ts)$`)

// HLS starts and stops Neko's broadcast and serves what it writes.
type HLS struct {
	folder, nekoURL, user, password string
	idle                            time.Duration

	mu        sync.Mutex
	token     string
	active    bool
	lastFetch time.Time
	watching  bool
	client    *http.Client
}

// New uses Neko at nekoURL (inside the Docker network) with its admin password, and the folder its
// broadcast writes to.
func New(folder, nekoURL, adminPassword string) *HLS {
	return &HLS{
		folder:   folder,
		nekoURL:  strings.TrimRight(nekoURL, "/"),
		user:     "streamiso",
		password: adminPassword,
		idle:     30 * time.Second,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Available: the folder is mounted and there is an admin password to start the broadcast with.
func (h *HLS) Available() bool {
	st, err := os.Stat(h.folder)
	return err == nil && st.IsDir() && h.password != ""
}

func (h *HLS) request(method, path string, body any, token string) (int, map[string]any, error) {
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.nekoURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("Neko did not answer: %v", err)
	}
	defer resp.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&data)
	return resp.StatusCode, data, nil
}

// call is an admin call. The session is kept, and opened again if Neko has forgotten it (a restart).
func (h *HLS) call(method, path string, body any) (int, map[string]any, error) {
	for i := 0; i < 2; i++ {
		if h.token == "" {
			status, data, err := h.request("POST", "/api/login", map[string]any{"username": h.user, "password": h.password}, "")
			if err != nil {
				return 0, nil, err
			}
			token, _ := data["token"].(string)
			if status != 200 || token == "" {
				return 0, nil, fmt.Errorf("Neko refused the admin login (%d)", status)
			}
			h.token = token
		}
		status, data, err := h.request(method, path, body, h.token)
		if err != nil || status != 401 {
			return status, data, err
		}
		h.token = ""
	}
	return 0, nil, errors.New("Neko refused the admin session")
}

// Start starts the broadcast, or keeps the one that is running.
func (h *HLS) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	status, data, err := h.call("GET", "/api/room/broadcast", nil)
	if err != nil {
		return err
	}
	if active, _ := data["is_active"].(bool); status == 200 && active {
		h.active = true
	} else {
		// Old segments and an old playlist would be played first.
		_ = os.Chmod(h.folder, 0o777) // Neko writes as its own user
		if entries, err := os.ReadDir(h.folder); err == nil {
			for _, e := range entries {
				if hlsFileName.MatchString(e.Name()) {
					_ = os.Remove(filepath.Join(h.folder, e.Name()))
				}
			}
		}
		// Neko needs a URL to start; the pipeline writes to the shared folder and ignores it.
		status, _, err := h.call("POST", "/api/room/broadcast/start", map[string]any{"url": "hls"})
		if err != nil {
			return err
		}
		if status != 200 && status != 204 && status != 422 { // 422: it was already broadcasting
			return fmt.Errorf("Neko did not start the broadcast (%d)", status)
		}
		h.active = true
	}
	h.lastFetch = time.Now()
	if !h.watching {
		h.watching = true
		go h.watch()
	}
	return nil
}

// WaitReady is true once the playlist lists two segments, enough for a TV to start playing.
func (h *HLS) WaitReady(timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if data, err := os.ReadFile(filepath.Join(h.folder, "index.m3u8")); err == nil && strings.Count(string(data), ".ts") >= 2 {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (h *HLS) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.active {
		return
	}
	h.active = false
	_, _, _ = h.call("POST", "/api/room/broadcast/stop", nil) // 422 if it had stopped already
}

// watch stops the broadcast when nobody fetches it: a TV fetches the playlist every second or two.
func (h *HLS) watch() {
	for {
		time.Sleep(5 * time.Second)
		h.mu.Lock()
		active, idle := h.active, time.Since(h.lastFetch) > h.idle
		if !active {
			h.watching = false
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		if idle {
			h.Stop()
		}
	}
}

// File is the playlist or a segment, if the name is one Neko writes and it exists. It counts as a fetch.
func (h *HLS) File(name string) string {
	if !hlsFileName.MatchString(name) {
		return ""
	}
	p := filepath.Join(h.folder, name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return ""
	}
	h.mu.Lock()
	h.lastFetch = time.Now()
	h.mu.Unlock()
	return p
}
