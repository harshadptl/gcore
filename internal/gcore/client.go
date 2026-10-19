// Package gcore is a small stdlib REST client for the Gcore Streaming API.
package gcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Stream is the subset of a Gcore stream object this app uses.
type Stream struct {
	ID          int64  `json:"id"`
	Active      bool   `json:"active"`
	Live        bool   `json:"live"`
	PushURL     string `json:"push_url"`
	PushURLSRT  string `json:"push_url_srt"`
	PushURLWHIP string `json:"push_url_whip"`
	IframeURL   string `json:"iframe_url"`
	HLSCMAFURL  string `json:"hls_cmaf_url"`
}

// APIError is a non-2xx reply from Gcore. Its text is shown to end users, so
// it carries only the status and (trimmed) response body, never the API key.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	b := e.Body
	if len(b) > 300 {
		b = b[:300]
	}
	return fmt.Sprintf("gcore: %d: %s", e.Status, b)
}

// Client talks to the Gcore Streaming API.
type Client struct {
	base, key string
	http      *http.Client
}

// New returns a client for baseURL authenticated with apiKey.
func New(baseURL, apiKey string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		key:  apiKey,
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{MaxIdleConnsPerHost: 20, IdleConnTimeout: 90 * time.Second},
		},
	}
}

// Create makes a new, inactive stream.
func (c *Client) Create(ctx context.Context, name string) (Stream, error) {
	var s Stream
	err := c.do(ctx, http.MethodPost, "/streaming/streams",
		struct {
			Name   string `json:"name"`
			Active bool   `json:"active"`
		}{name, false}, &s)
	return s, err
}

// SetActive turns a stream's ingest on or off.
func (c *Client) SetActive(ctx context.Context, id int64, active bool) (Stream, error) {
	var s Stream
	err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/streaming/streams/%d", id), map[string]any{"active": active}, &s)
	return s, err
}

// ponytail: GET /streaming/streams returns one page; if the Gcore
// account ever holds enough streams to paginate, this stops seeing all
// of them. Upgrade to a pagination loop if/when that's true — not
// expected for this app's scale.

// List returns the account's streams.
func (c *Client) List(ctx context.Context) ([]Stream, error) {
	var s []Stream
	err := c.do(ctx, http.MethodGet, "/streaming/streams", nil, &s)
	return s, err
}

// Delete removes a stream.
func (c *Client) Delete(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/streaming/streams/%d", id), nil, nil)
}

// do sends one request; out may be nil when no body is expected.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "APIKey "+c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error holds only method and URL, never headers.
		return fmt.Errorf("gcore %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("gcore read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("gcore decode: %w", err)
	}
	return nil
}
