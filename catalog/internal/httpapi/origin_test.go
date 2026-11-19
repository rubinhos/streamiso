package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"streamiso/internal/config"
)

func guarded(cfg config.Config, method, target, origin string, header map[string]string) (*httptest.ResponseRecorder, bool) {
	s := &Server{cfg: cfg}
	r := httptest.NewRequest(method, target, nil)
	r.Host = "192.168.1.4:8080"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	return w, s.guard(w, r)
}

func TestGuard(t *testing.T) {
	keyed := config.Config{APIKey: "s3cret"}
	closed := config.Config{}
	cases := []struct {
		name    string
		cfg     config.Config
		method  string
		target  string
		origin  string
		header  map[string]string
		allowed bool
		cors    string // Access-Control-Allow-Origin expected ("" for none)
	}{
		{"no origin (native app, curl)", closed, "POST", "/api/stop", "", nil, true, ""},
		{"same origin", closed, "POST", "/api/stop", "http://192.168.1.4:8080", nil, true, ""},
		{"same origin through a proxy", closed, "POST", "/api/stop", "https://player.example",
			map[string]string{"X-Forwarded-Host": "player.example"}, true, ""},
		{"other website", closed, "POST", "/api/stop", "https://evil.example", nil, false, ""},
		{"other port of the same host", closed, "POST", "/api/stop", "http://192.168.1.4:8081", nil, false, ""},
		{"sandboxed frame or TV app, no key set", closed, "GET", "/api/player", "null", nil, false, ""},
		{"pages outside /api/ are not checked", closed, "GET", "/player", "https://evil.example", nil, true, ""},

		{"key: null without key", keyed, "GET", "/api/player", "null", nil, false, "null"},
		{"key: null with key", keyed, "GET", "/api/player", "null", map[string]string{keyHeader: "s3cret"}, true, "null"},
		{"key: wrong key", keyed, "GET", "/api/player", "null", map[string]string{keyHeader: "nope"}, false, "null"},
		{"key: in the query (sendBeacon)", keyed, "POST", "/api/stop?key=s3cret", "null", nil, true, "null"},
		{"key: other website without key", keyed, "POST", "/api/stop", "https://evil.example", nil, false, "https://evil.example"},
		{"key: preflight is answered", keyed, "OPTIONS", "/api/discs", "null", nil, true, "null"},
		{"key: same origin needs no key", keyed, "POST", "/api/stop", "http://192.168.1.4:8080", nil, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, ok := guarded(c.cfg, c.method, c.target, c.origin, c.header)
			if ok != c.allowed {
				t.Fatalf("allowed = %v, want %v (status %d, %s)", ok, c.allowed, w.Code, w.Body.String())
			}
			if !ok && w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != c.cors {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, c.cors)
			}
		})
	}
}
