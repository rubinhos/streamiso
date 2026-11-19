package httpapi

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// Which callers may use the API. There is no login, so a browser must not let another website use it: a page
// anywhere on the internet, opened on the same network, could otherwise start or stop discs, edit the library
// or read the player's password from /api/player.
//
// A browser names the page behind every request it sends across origins (and every POST) in the Origin
// header. A request to /api/ is let through when:
//   - it has no Origin: not a browser page (the Android app's own calls, curl), or a same-origin GET;
//   - its Origin is this server, as the browser reached it (the catalog's own pages);
//   - its Origin is listed by name in CORS_ORIGIN;
//   - it carries API_KEY, in the X-StreamISO-Key header or the "key" query parameter (for sendBeacon, which
//     cannot set headers). This is how the TV apps call it: they run from file:// and send the origin "null",
//     which any website can send too, from a sandboxed frame;
//   - API_KEY is not set and CORS_ORIGIN lists "null" or "*", as before API_KEY existed (main warns).
//
// Images (<img>) and the plain video (<video>) are fetched without an Origin, so they keep working anywhere.

const keyHeader = "X-StreamISO-Key"

// sameOrigin: the page's origin is this server, by the address the browser used (Host, or the Host a reverse
// proxy was asked for).
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	for _, h := range []string{r.Host, r.Header.Get("X-Forwarded-Host")} {
		if h = strings.ToLower(strings.TrimSpace(strings.Split(h, ",")[0])); h != "" && h == host {
			return true
		}
	}
	return false
}

func (s *Server) keyOK(r *http.Request) bool {
	if s.cfg.APIKey == "" {
		return false
	}
	key := r.Header.Get(keyHeader)
	if key == "" {
		key = r.URL.Query().Get("key")
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.APIKey)) == 1
}

// listed: CORS_ORIGIN names this origin. "null" and "*" only count while there is no API_KEY.
func (s *Server) listed(origin string) bool {
	for _, o := range s.cfg.CORSOrigins {
		if o == origin && o != "null" {
			return true
		}
		if (o == "*" || o == "null" && origin == "null") && s.cfg.APIKey == "" {
			return true
		}
	}
	return false
}

// LegacyOpenCORS: CORS_ORIGIN lets any website call the API, because it lists "null" or "*" and there is no
// API_KEY.
func (s *Server) LegacyOpenCORS() bool {
	return s.cfg.APIKey == "" && (s.listed("null") || s.listed("*"))
}

// guard checks a request to /api/ (see above) and sets the CORS headers. false means it was answered already:
// a preflight, or a refusal (403).
func (s *Server) guard(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" || sameOrigin(origin, r) {
		return true
	}
	listed := s.listed(origin)
	// With an API key, every origin gets the CORS headers, so an app can read why it was refused; only the key
	// lets it do anything. A preflight carries no key (only the name of its header), so it is always answered.
	if listed || s.cfg.APIKey != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+keyHeader)
		w.Header().Set("Access-Control-Max-Age", "600")
	}
	if r.Method == http.MethodOptions {
		return true // answered by the router, with the CORS headers or without them
	}
	if listed || s.keyOK(r) {
		return true
	}
	msg := "This address does not accept calls from other websites"
	if s.cfg.APIKey != "" {
		msg = "Missing or wrong API key (API_KEY in the server's .env)"
	}
	jsonError(w, http.StatusForbidden, msg)
	return false
}
