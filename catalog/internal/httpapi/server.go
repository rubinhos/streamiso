// Package httpapi is the catalog's HTTP side: the web page and the JSON API (see docs/API.md). Each
// handler reads the request, calls a use case in app and writes its answer.
package httpapi

import (
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"streamiso/internal/app"
	"streamiso/internal/compat"
	"streamiso/internal/config"
	"streamiso/internal/domain"
)

type Server struct {
	cfg      config.Config
	catalog  *app.Catalog
	library  *app.Library
	metadata *app.Metadata
	playback *app.Playback
}

func New(cfg config.Config, catalog *app.Catalog, library *app.Library, metadata *app.Metadata, playback *app.Playback) *Server {
	for ext, typ := range map[string]string{
		".ico": "image/vnd.microsoft.icon", ".webmanifest": "application/manifest+json",
		".svg": "image/svg+xml", ".webp": "image/webp", ".js": "text/javascript; charset=utf-8",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
	return &Server{cfg: cfg, catalog: catalog, library: library, metadata: metadata, playback: playback}
}

// ---------------------------------------------------------------------------------- responses

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(compat.MarshalJSON(v, ""), '\n'))
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// answerError writes a use case's failure, or fails the request (500) for anything else.
func answerError(w http.ResponseWriter, err error) {
	var f *app.Failure
	if errors.As(err, &f) {
		jsonError(w, f.Status, f.Msg)
		return
	}
	panic(err)
}

var httpErrorText = map[int][2]string{
	404: {"Not Found", "The requested URL was not found on the server. If you entered the URL manually please check your spelling and try again."},
	405: {"Method Not Allowed", "The method is not allowed for the requested URL."},
	413: {"Request Entity Too Large", "The data value transmitted exceeds the capacity limit."},
	500: {"Internal Server Error", "The server encountered an internal error and was unable to complete your request. Either the server is overloaded or there is an error in the application."},
}

// htmlError is the error page for a status that is not the API's own (a missing page, a body too large).
func htmlError(w http.ResponseWriter, status int) {
	t := httpErrorText[status]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html>\n<html lang=en>\n<title>%d %s</title>\n<h1>%s</h1>\n<p>%s</p>\n", status, t[0], t[0], t[1])
}

// serveFile sends a file: maxAge < 0 means "no-cache", and ctype "" guesses the type from its name.
func serveFile(w http.ResponseWriter, r *http.Request, p string, maxAge int, ctype string) {
	f, err := os.Open(p)
	if err != nil {
		htmlError(w, 404)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		htmlError(w, 404)
		return
	}
	if ctype == "" {
		ctype = mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		if (strings.HasPrefix(ctype, "text/") || strings.HasSuffix(ctype, "+xml")) && !strings.Contains(ctype, "charset") {
			ctype += "; charset=utf-8"
		}
	}
	w.Header().Set("Content-Type", ctype)
	if maxAge >= 0 {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
		w.Header().Set("Expires", time.Now().Add(time.Duration(maxAge)*time.Second).UTC().Format(http.TimeFormat))
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// ---------------------------------------------------------------------------------- requests

const maxContentLength = domain.MaxCoverBytes + 256*1024

var errTooLarge = errors.New("too large")

// getJSON is the JSON body, or nil unless the request says it is JSON and it is.
func getJSON(r *http.Request) (any, error) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !(ct == "application/json" || (strings.HasPrefix(ct, "application/") && strings.HasSuffix(ct, "+json"))) {
		return nil, nil
	}
	if r.ContentLength > maxContentLength {
		return nil, errTooLarge
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxContentLength))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errTooLarge
		}
		return nil, nil
	}
	v, err := compat.DecodeJSON(data)
	if err != nil {
		return nil, nil
	}
	return v, nil
}

// jsonBody reads the JSON body as an object (nil when it is not one). false means the response
// (413) was already sent.
func jsonBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	v, err := getJSON(r)
	if err == errTooLarge {
		htmlError(w, 413)
		return nil, false
	}
	m, _ := v.(map[string]any)
	return m, true
}

// disc is the disc the path names; nil means the 404 was already sent.
func (s *Server) disc(w http.ResponseWriter, r *http.Request) *domain.Disc {
	d := s.catalog.Disc(r.PathValue("id"))
	if d == nil {
		htmlError(w, 404)
	}
	return d
}

// ---------------------------------------------------------------------------------- routing

func (s *Server) staticFile(name string) string {
	return filepath.Join(s.cfg.StaticDir, filepath.FromSlash(name))
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	page := func(name string, maxAge int, ctype string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { serveFile(w, r, s.staticFile(name), maxAge, ctype) }
	}
	mux.HandleFunc("GET /{$}", page("index.html", -1, ""))
	mux.HandleFunc("GET /favicon.ico", page("icons/favicon.ico", 86400, ""))
	mux.HandleFunc("GET /manifest.webmanifest", page("manifest.webmanifest", -1, "application/manifest+json"))
	mux.HandleFunc("GET /player", page("player.html", -1, ""))
	mux.HandleFunc("GET /static/{path...}", func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.PathValue("path"))
		serveFile(w, r, s.staticFile(strings.TrimPrefix(p, "/")), -1, "")
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": config.Version})
	})
	mux.HandleFunc("GET /api/discs", s.handleDiscs)
	mux.HandleFunc("GET /api/cover/{id}", s.handleCover)
	mux.HandleFunc("PUT /api/disc/{id}/meta", s.handleMetaPut)
	mux.HandleFunc("POST /api/disc/{id}/watched", s.handleWatched)
	mux.HandleFunc("POST /api/disc/{id}/lookup", s.handleLookup)
	mux.HandleFunc("POST /api/disc/{id}/resume/clear", s.handleResumeClear)
	mux.HandleFunc("GET /api/disc/{id}/posters", s.handlePosters)
	mux.HandleFunc("POST /api/disc/{id}/poster", s.handlePosterSet)
	mux.HandleFunc("POST /api/disc/{id}/cover", s.handleCoverUpload)
	mux.HandleFunc("DELETE /api/disc/{id}/cover", s.handleCoverDelete)
	mux.HandleFunc("POST /api/play/{id}", s.handlePlay)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("POST /api/stream/hls", s.handleStreamHLS)
	mux.HandleFunc("GET /api/hls/{name}", s.handleHLSFile)
	mux.HandleFunc("GET /api/player", s.handlePlayer)
	mux.HandleFunc("GET /api/player/status", s.handlePlayerStatus)
	mux.HandleFunc("POST /api/player/cmd", s.handlePlayerCmd)
	mux.HandleFunc("POST /api/rescan", s.handleRescan)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("ERROR: %s %s: %v", r.Method, r.URL.Path, rec)
				htmlError(w, 500)
			}
		}()
		s.addCORS(w, r)
		if r.Method == http.MethodOptions { // CORS preflights
			if allow := allowedMethods(mux, r); allow != "" {
				w.Header().Set("Allow", allow)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(200)
				return
			}
			htmlError(w, 404)
			return
		}
		if _, pattern := mux.Handler(r); pattern == "" { // the HTML error pages, not Go's plain text
			if allow := allowedMethods(mux, r); allow != "" {
				w.Header().Set("Allow", allow)
				htmlError(w, 405)
				return
			}
			htmlError(w, 404)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// allowedMethods lists the methods a path answers to, for the Allow header.
func allowedMethods(mux *http.ServeMux, r *http.Request) string {
	var out []string
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" && strings.HasPrefix(pattern, m+" ") {
			out = append(out, m)
			if m == "GET" {
				out = append(out, "HEAD")
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return strings.Join(append(out, "OPTIONS"), ", ")
}

// addCORS lets other apps (the TV apps) call the API from the origins in CORS_ORIGIN.
func (s *Server) addCORS(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.CORSOrigins) == 0 || !strings.HasPrefix(r.URL.Path, "/api/") {
		return
	}
	origin := r.Header.Get("Origin")
	allowed := false
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			allowed = true
			break
		}
	}
	if !allowed {
		for _, o := range s.cfg.CORSOrigins {
			if o == origin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				allowed = true
				break
			}
		}
	}
	if allowed {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
}
