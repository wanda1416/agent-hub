package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://aihot.news"
	// userAgent is the value AIHOT's API guide asks programmatic clients to
	// send, so traffic is identifiable and rate-limit policies apply cleanly.
	userAgent      = "aihot-api/2.0.0"
	requestTimeout = 15 * time.Second
	maxBodyBytes   = 4 << 20
)

// apiResponse mirrors the fields of the resets endpoint that the monitor uses.
type apiResponse struct {
	CheckedAt string       `json:"checkedAt"`
	Count     int          `json:"count"`
	Events    []Event      `json:"events"`
	Monitor   *SiteMonitor `json:"monitor"`
}

type fetchResult struct {
	Events      []Event
	CheckedAt   string
	Monitor     *SiteMonitor
	ETag        string
	NotModified bool
}

// Client talks to the AIHOT JSON API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a client for baseURL. An empty baseURL uses
// AGENTHUB_AIHOT_BASE or the public site, which is also the hook used by tests.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = os.Getenv("AGENTHUB_AIHOT_BASE")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// Fetch retrieves the recent resets of one model. When etag matches the
// server's current representation the response is a 304 with no body.
func (c *Client) Fetch(ctx context.Context, model Model, etag string) (*fetchResult, error) {
	url := fmt.Sprintf("%s/api/v1/%s-resets/recent", c.baseURL, model.Slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &fetchResult{NotModified: true, ETag: etag}, nil
	}
	if resp.StatusCode != http.StatusOK {
		// Drain a little of the body so the connection can be reused, and
		// include the status in the error surfaced in the UI.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		detail := strings.TrimSpace(string(snippet))
		if detail != "" {
			return nil, fmt.Errorf("%s: %s", resp.Status, detail)
		}
		return nil, fmt.Errorf("%s", resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	var payload apiResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &fetchResult{
		Events:    payload.Events,
		CheckedAt: payload.CheckedAt,
		Monitor:   payload.Monitor,
		ETag:      resp.Header.Get("ETag"),
	}, nil
}
