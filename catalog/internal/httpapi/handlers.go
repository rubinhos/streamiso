package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"streamiso/internal/compat"
	"streamiso/internal/domain"
)

// ---------------------------------------------------------------------------------- library

func (s *Server) handleDiscs(w http.ResponseWriter, r *http.Request) {
	discs, genres, aliases, playing := s.library.Discs()
	mediaDir := s.cfg.DVDDir
	if s.cfg.DVDDir != s.cfg.CDDir {
		mediaDir = s.cfg.DVDDir + " and " + s.cfg.CDDir
	}
	writeJSON(w, 200, map[string]any{"discs": discs, "genres": genres, "genre_aliases": aliases, "media_dir": mediaDir,
		"tmdb": s.metadata.TMDB(), "playing": playing})
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	file, maxAge, kind, placeholder := s.library.Cover(d)
	if file != "" {
		serveFile(w, r, file, maxAge, kind)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, placeholder)
}

func (s *Server) handleMetaPut(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	if payload == nil {
		jsonError(w, 400, "Invalid JSON")
		return
	}
	view, err := s.library.SaveEdits(d, payload)
	if err != nil {
		answerError(w, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleWatched(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	watched := payload != nil && compat.Truthy(payload["watched"])
	if err := s.library.SetWatched(d, watched); err != nil {
		panic(err)
	}
	writeJSON(w, 200, map[string]any{"watched": watched})
}

func (s *Server) handleResumeClear(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	s.library.ForgetResume(d)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCoverUpload(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxContentLength)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			htmlError(w, 413)
			return
		}
	}
	f, _, err := r.FormFile("cover")
	if err != nil {
		jsonError(w, 400, "Missing cover file")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, domain.MaxCoverBytes+1))
	if err != nil {
		panic(err)
	}
	if err := s.library.SaveCover(d, data); err != nil {
		answerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCoverDelete(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	s.library.DeleteCover(d)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"count": s.catalog.Get(true).Len()})
}

// ---------------------------------------------------------------------------------- lookups

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	query := ""
	if payload != nil {
		query = compat.Slice(compat.Strip(compat.Str(compat.GetOr(payload, "query", ""))), 200)
	}
	found, cover, err := s.metadata.Lookup(d, query)
	if err != nil {
		answerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"found": found, "cover": cover, "disc": s.library.View(d)})
}

var thumbRE = regexp.MustCompile(`/w\d+$`)

func (s *Server) handlePosters(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	if err := s.metadata.CheckPosters(d); err != nil {
		answerError(w, err)
		return
	}
	thumbURL := thumbRE.ReplaceAllString(s.cfg.TMDBImg, "/w185") // TMDB_IMAGE_URL's "w500" becomes "w185"
	q := r.URL.Query()
	kind, tmdbID := q.Get("tmdb_kind"), q.Get("tmdb_id")
	if kind != "" || tmdbID != "" { // only the posters of one title: ?tmdb_kind=movie&tmdb_id=597
		if (kind != "tv" && kind != "movie") || !compat.IsAllDigits(tmdbID) {
			jsonError(w, 400, "Invalid title")
			return
		}
		id, _ := strconv.ParseInt(tmdbID, 10, 64)
		posters, err := s.metadata.TitlePosters(d, kind, id)
		if err != nil {
			answerError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"posters": posters, "thumb_url": thumbURL})
		return
	}
	out, err := s.metadata.PosterChoices(d, compat.Slice(compat.Strip(q.Get("query")), 200))
	if err != nil {
		answerError(w, err)
		return
	}
	out["thumb_url"] = thumbURL // smaller images for the thumbnails
	out["covered"] = s.library.Covered(d)
	writeJSON(w, 200, out)
}

func (s *Server) handlePosterSet(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	if err := s.metadata.CheckPosters(d); err != nil {
		answerError(w, err)
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	if err := s.metadata.SetPoster(d, payload); err != nil {
		answerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"disc": s.library.View(d), "covered": s.library.Covered(d)})
}

// ---------------------------------------------------------------------------------- playback

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	d := s.disc(w, r)
	if d == nil {
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	out, err := s.playback.Play(d, payload != nil && compat.Truthy(payload["resume"]))
	if err != nil {
		panic(err)
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.playback.Stop(); err != nil {
		panic(err)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// nekoURL is the address of the player's stream page, as the client reaches the server.
func (s *Server) nekoURL(r *http.Request) string {
	query := "usr=" + url.QueryEscape(s.cfg.NekoUser) + "&pwd=" + url.QueryEscape(s.cfg.NekoPassword) + "&cast=1"
	if s.cfg.NekoPublicURL != "" {
		return s.cfg.NekoPublicURL + "/?" + query
	}
	host := r.Host
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			host = host[1:end]
		}
	} else if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	if host == "" {
		host = "localhost"
	}
	if strings.Contains(host, ":") { // IPv6
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + s.cfg.NekoPort + "/?" + query
}

func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	out := s.playback.Info()
	out["url"] = s.nekoURL(r)
	writeJSON(w, 200, out)
}

func (s *Server) handlePlayerStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.playback.Status()
	if err != nil {
		panic(err)
	}
	writeJSON(w, 200, st)
}

func (s *Server) handlePlayerCmd(w http.ResponseWriter, r *http.Request) {
	if !s.playback.IsOpen() {
		jsonError(w, 409, "Nothing is playing")
		return
	}
	payload, ok := jsonBody(w, r)
	if !ok {
		return
	}
	if err := s.playback.Command(payload); err != nil {
		answerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleStreamHLS(w http.ResponseWriter, r *http.Request) {
	u, err := s.playback.StartPlainVideo()
	if err != nil {
		answerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"url": u})
}

func (s *Server) handleHLSFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p := s.playback.PlainVideoFile(name)
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
