// Package httpx fetches URLs for the adapters that call outside services.
package httpx

import (
	"io"
	"net/http"
	"streamiso/internal/ports"
	"time"
)

var httpClient = &http.Client{}

// Get fetches a URL (following redirects). limit > 0 reads at most limit+1 bytes.
func Get(rawURL string, headers map[string]string, timeout time.Duration, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c := *httpClient
	c.Timeout = timeout
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return nil, &ports.HTTPError{Code: resp.StatusCode, URL: rawURL}
	}
	var r io.Reader = resp.Body
	if limit > 0 {
		r = io.LimitReader(resp.Body, limit+1)
	}
	return io.ReadAll(r)
}
