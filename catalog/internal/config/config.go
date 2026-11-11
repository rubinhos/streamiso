// Package config reads the settings from the environment (set by docker-compose.yml from .env).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"streamiso/internal/compat"
)

const Version = "0.6.0"

type Config struct {
	DVDDir, CDDir, BlurayDir       string
	DataDir, StateFile             string
	VLCHost                        string
	VLCPort                        int
	NekoPort, NekoPublicURL        string
	NekoUser, NekoPassword         string
	NekoInternalURL, NekoAdminPass string
	HLSDir                         string
	CORSOrigins                    []string
	TMDBKey, TMDBLanguage          string
	TMDBAPI, TMDBImg               string
	MBURL, CAAURL, MBContact       string
	Port, StaticDir                string

	// Under DataDir
	CoverDir       string // covers downloaded from TMDB or the Cover Art Archive
	CustomCoverDir string // covers uploaded by the user
	MetaDir        string // what the lookups found
	LibFile        string // the user's edits
	ResumeFile     string // where each disc was left
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// cleanPath has no trailing slash, no "." parts and no doubled slashes.
func cleanPath(p string) string {
	if p == "" {
		return "."
	}
	return filepath.Clean(p)
}

func Load() (Config, error) {
	var c Config
	media := env("MEDIA_DIR", "")
	first := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	c.DVDDir = cleanPath(first(os.Getenv("DVD_DIR"), media, "/media/dvds"))
	c.CDDir = cleanPath(first(os.Getenv("CD_DIR"), media, "/media/cds"))
	c.BlurayDir = cleanPath(first(os.Getenv("BLURAY_DIR"), media, "/media/blurays"))
	c.DataDir = cleanPath(env("DATA_DIR", "/data"))
	c.StateFile = cleanPath(env("STATE_FILE", "/state/current"))
	c.VLCHost = env("VLC_RC_HOST", "neko")
	port, err := strconv.Atoi(env("VLC_RC_PORT", "4212"))
	if err != nil {
		return c, fmt.Errorf("VLC_RC_PORT: %v", err)
	}
	c.VLCPort = port
	c.NekoPort = env("NEKO_PORT", "8081")
	c.NekoPublicURL = strings.TrimRight(env("NEKO_PUBLIC_URL", ""), "/")
	c.NekoUser = env("NEKO_USER", "tv")
	c.NekoPassword = env("NEKO_PASSWORD", "neko")
	c.NekoInternalURL = strings.TrimRight(env("NEKO_INTERNAL_URL", "http://neko:8080"), "/")
	c.NekoAdminPass = env("NEKO_ADMIN_PASSWORD", "")
	c.HLSDir = env("HLS_DIR", "/hls")
	for _, o := range strings.Split(env("CORS_ORIGIN", ""), ",") {
		if o = compat.Strip(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	c.TMDBKey = compat.Strip(env("TMDB_API_KEY", ""))
	c.TMDBLanguage = env("TMDB_LANGUAGE", "en-US")
	c.TMDBAPI = strings.TrimRight(env("TMDB_API_URL", "https://api.themoviedb.org/3"), "/")
	c.TMDBImg = strings.TrimRight(env("TMDB_IMAGE_URL", "https://image.tmdb.org/t/p/w500"), "/")
	c.MBURL = strings.TrimRight(env("MUSICBRAINZ_URL", "https://musicbrainz.org/ws/2"), "/")
	c.CAAURL = strings.TrimRight(env("COVERART_URL", "https://coverartarchive.org"), "/")
	c.MBContact = compat.Strip(env("MUSICBRAINZ_CONTACT", ""))
	c.Port = env("PORT", "8080")
	c.StaticDir = env("STATIC_DIR", "static")
	c.CoverDir = filepath.Join(c.DataDir, "covers")
	c.CustomCoverDir = filepath.Join(c.DataDir, "covers_custom")
	c.MetaDir = filepath.Join(c.DataDir, "meta")
	c.LibFile = filepath.Join(c.DataDir, "library.json")
	c.ResumeFile = filepath.Join(c.DataDir, "resume.json")
	return c, nil
}
