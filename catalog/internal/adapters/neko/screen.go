package neko

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize reads "1920x1080" (as STREAM_RESOLUTION and BLURAY_RESOLUTION are written); ok is false otherwise.
func ParseSize(s string) (width, height int, ok bool) {
	w, h, found := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "x")
	if !found {
		return 0, 0, false
	}
	width, err1 := strconv.Atoi(w)
	height, err2 := strconv.Atoi(h)
	if err1 != nil || err2 != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

// SetScreenSize changes the size of the screen VLC draws on, keeping its refresh rate. Neko then rebuilds
// the video encoding at the new size, and VLC, full screen, follows. Nothing is done if it already has it.
// It implements ports.Screen.
func (h *HLS) SetScreenSize(width, height int) error {
	if h.password == "" {
		return fmt.Errorf("no NEKO_ADMIN_PASSWORD to change the screen with")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	status, cur, err := h.call("GET", "/api/room/screen", nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("Neko did not give the screen size (%d)", status)
	}
	if w, _ := cur["width"].(float64); int(w) == width {
		if hh, _ := cur["height"].(float64); int(hh) == height {
			return nil
		}
	}
	rate := cur["rate"]
	status, _, err = h.call("POST", "/api/room/screen", map[string]any{"width": width, "height": height, "rate": rate})
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("Neko refused the screen size %dx%d@%v (%d): it must be one the screen offers", width, height, rate, status)
	}
	return nil
}
