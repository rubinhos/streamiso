// Package tmdb calls The Movie Database's API and fetches its poster images. It implements ports.MovieDB;
// what to search for and how to read the answers is the app's (app.Metadata).
package tmdb

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"streamiso/internal/adapters/httpx"
	"streamiso/internal/compat"
	"streamiso/internal/config"
)

type Client struct {
	API, ImageURL, Key string
}

func (c *Client) headers() map[string]string {
	return map[string]string{"User-Agent": "streamiso/" + config.Version}
}

// Get calls the API. params are added in order, then the key.
func (c *Client) Get(path string, params ...[2]string) (map[string]any, error) {
	q := make([]string, 0, len(params)+1)
	for _, p := range params {
		q = append(q, url.QueryEscape(p[0])+"="+url.QueryEscape(p[1]))
	}
	q = append(q, "api_key="+url.QueryEscape(c.Key))
	body, err := httpx.Get(c.API+path+"?"+strings.Join(q, "&"), c.headers(), 15*time.Second, 0)
	if err != nil {
		return nil, err
	}
	v, err := compat.DecodeJSON(body)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("TMDB sent no object")
	}
	return m, nil
}

// Image downloads a poster, by the path TMDB gives for it ("/abc.jpg").
func (c *Client) Image(path string) ([]byte, error) {
	return httpx.Get(c.ImageURL+path, c.headers(), 20*time.Second, 0)
}
