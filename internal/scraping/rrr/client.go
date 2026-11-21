// Package rrr reads rrr.lt, a marketplace of used car parts, through the
// GraphQL API its own pages use (no HTML scraping). A search picks part
// categories and donor cars; each search is one API query, sorted newest
// first, which the site filters itself.
package rrr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/metrics"
)

// Source is the value of events.ListingDiscovered.Source for rrr.lt.
const Source = "rrr.lt"

const (
	baseURL   = "https://rrr.lt"
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
	// maxBody caps an API response; a page of parts is ~30 KB, the whole
	// taxonomy ~600 KB.
	maxBody = 8 << 20
)

// StatusError is returned for HTTP responses >= 400. The scraper uses the
// code to decide whether to back off (429, 403, 5xx).
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("fetch %s: status %d", e.URL, e.Code) }

// HTTPStatus lets the scraper tell "the site is pushing back" from other errors.
func (e *StatusError) HTTPStatus() int { return e.Code }

// Client queries rrr.lt's API.
type Client struct {
	http *http.Client
	base string

	// gap spaces out requests the client throttles itself (codebook builds).
	// Feed pages are paced by the scraper's runner.
	gap     time.Duration
	mu      sync.Mutex
	lastReq time.Time
}

func NewClient(timeout time.Duration) *Client {
	return &Client{http: &http.Client{Timeout: timeout}, base: baseURL}
}

// WithBaseURL points the client at another host (a local fake in tests).
func (c *Client) WithBaseURL(u string) *Client {
	c.base = strings.TrimRight(u, "/")
	return c
}

// WithGap makes the client wait at least gap between the requests it
// throttles itself.
func (c *Client) WithGap(gap time.Duration) *Client {
	c.gap = gap
	return c
}

// Name implements sources.Source.
func (c *Client) Name() string { return Source }

// throttle waits until gap has passed since the previous throttled request.
func (c *Client) throttle(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if wait := c.gap - time.Since(c.lastReq); wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	c.lastReq = time.Now()
	return nil
}

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// query runs one GraphQL query and decodes its data into out. A response
// with errors and no data is an error; partial data is used as it is.
func (c *Client) query(ctx context.Context, q string, vars map[string]any, out any) error {
	body, err := json.Marshal(gqlRequest{Query: q, Variables: vars})
	if err != nil {
		return err
	}
	url := c.base + "/graphql"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/en")

	resp, err := c.http.Do(req)
	if err != nil {
		metrics.SiteRequestFailed(Source)
		return fmt.Errorf("query %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	metrics.SiteRequest(Source, resp.StatusCode)

	if resp.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return &StatusError{URL: url, Code: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("read %s: %w", url, err)
	}
	var r gqlResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	if len(r.Errors) > 0 && (len(r.Data) == 0 || string(r.Data) == "null") {
		msgs := make([]string, len(r.Errors))
		for i, e := range r.Errors {
			msgs[i] = e.Message
		}
		return errors.New("rrr.lt api: " + strings.Join(msgs, "; "))
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		return fmt.Errorf("decode %s data: %w", url, err)
	}
	return nil
}
