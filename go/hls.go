package main

// Plain video (HLS) for TVs whose WebRTC cannot connect to Neko, like hls.py in the Python catalog.
//
// Some TVs have WebRTC too old for Neko: webOS 3 (Chrome 38) only speaks DTLS 1.0, and Neko needs DTLS 1.2.
// They can still play an ordinary video stream. docker-compose.yml sets Neko's broadcast to write the same
// screen and sound it streams over WebRTC as HLS segments, in a folder it shares with the catalog. This
// starts that broadcast through Neko's API when such a TV asks, serves its files, and stops it once nobody
// has fetched them for a while, so the extra encoding only runs while someone watches this way.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

type hlsBroadcast struct {
	folder, nekoURL, user, password string
	idle                            time.Duration

	mu        sync.Mutex
	token     string
	active    bool
	lastFetch time.Time
	watching  bool
	client    *http.Client
}

var hls *hlsBroadcast

func newHLS() *hlsBroadcast {
	return &hlsBroadcast{
		folder:   env("HLS_DIR", "/hls"),
		nekoURL:  strings.TrimRight(env("NEKO_INTERNAL_URL", "http://neko:8080"), "/"),
		user:     "streamiso",
		password: env("NEKO_ADMIN_PASSWORD", ""),
		idle:     30 * time.Second,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// available: the folder is mounted and there is an admin password to start the broadcast with.
func (h *hlsBroadcast) available() bool {
	st, err := os.Stat(h.folder)
	return err == nil && st.IsDir() && h.password != ""
}

func (h *hlsBroadcast) request(method, path string, body any, token string) (int, map[string]any, error) {
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
func (h *hlsBroadcast) call(method, path string, body any) (int, map[string]any, error) {
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

// start starts the broadcast, or keeps the one that is running.
func (h *hlsBroadcast) start() error {
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

// waitReady is true once the playlist lists two segments, enough for a TV to start playing.
func (h *hlsBroadcast) waitReady(timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if data, err := os.ReadFile(filepath.Join(h.folder, "index.m3u8")); err == nil && strings.Count(string(data), ".ts") >= 2 {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (h *hlsBroadcast) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.active {
		return
	}
	h.active = false
	_, _, _ = h.call("POST", "/api/room/broadcast/stop", nil) // 422 if it had stopped already
}

// watch stops the broadcast when nobody fetches it: a TV fetches the playlist every second or two.
func (h *hlsBroadcast) watch() {
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
			h.stop()
		}
	}
}

// file is the playlist or a segment, if the name is one Neko writes and it exists. It counts as a fetch.
func (h *hlsBroadcast) file(name string) string {
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

func handleStreamHLS(w http.ResponseWriter, r *http.Request) {
	if !hls.available() {
		jsonError(w, 501, "Plain video is not set up on this server: see HLS in the README")
		return
	}
	if err := hls.start(); err != nil {
		log.Printf("WARNING: Plain video: %v", err)
		jsonError(w, 502, err.Error())
		return
	}
	if !hls.waitReady(10 * time.Second) {
		jsonError(w, 504, "The plain video did not start")
		return
	}
	writeJSON(w, 200, map[string]any{"url": "/api/hls/index.m3u8"})
}

func handleHLSFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p := hls.file(name)
	if p == "" {
		htmlError(w, 404)
		return
	}
	kind := "video/mp2t"
	if strings.HasSuffix(name, ".m3u8") {
		kind = "application/vnd.apple.mpegurl"
	}
	serveFile(w, r, p, -1, kind)
}
