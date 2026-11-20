// Package pages reads prices from arbitrary product pages and JSON APIs for
// price watches: it fetches a page (only from public addresses), finds the
// picked element or JSON value, parses the price, suggests where a price
// might be, and prepares a script-free copy of a page for the dashboard's
// element picker.
package pages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

const (
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	// maxBody caps a page read. Shops' pages with inline app state reach a
	// few MB; the example Strandberg page is about 1 MB.
	maxBody      = 8 << 20
	maxRedirects = 5
	maxURLLen    = 2000
)

// ErrPrivateAddress: the URL points into a private network (this server's
// own services, a router, cloud metadata). Pages are only read from the
// public internet.
var ErrPrivateAddress = errors.New("that address is on a private network")

// Page is one fetched response.
type Page struct {
	URL         string // after redirects
	Status      int
	ContentType string // media type only, e.g. "text/html"
	Body        []byte // UTF-8 for text types
	FetchedAt   time.Time
}

// IsJSON reports whether the response is JSON rather than a web page.
func (p *Page) IsJSON() bool {
	return p.ContentType == "application/json" || strings.HasSuffix(p.ContentType, "+json") ||
		(p.ContentType == "text/plain" && looksLikeJSON(p.Body))
}

func looksLikeJSON(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && (b[0] == '{' || b[0] == '[')
}

// Client fetches pages, one request per host per gap.
type Client struct {
	http         *http.Client
	gap          time.Duration
	allowPrivate bool

	mu   sync.Mutex
	last map[string]time.Time // per host
}

// New returns a client that refuses private addresses. timeout bounds each
// request; gap spaces out requests to the same host.
func New(timeout, gap time.Duration) *Client {
	c := &Client{gap: gap, last: map[string]time.Time{}}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: c.checkAddress}
	c.http = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil, // a proxy would hide where requests really go
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          10,
			IdleConnTimeout:       time.Minute,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to %s: not a web page", req.URL.Scheme)
			}
			return nil
		},
	}
	return c
}

// AllowPrivate lets the client read private addresses: for tests against
// local servers only.
func (c *Client) AllowPrivate() *Client {
	c.allowPrivate = true
	return c
}

// checkAddress runs after DNS resolution, for every connection including
// redirects, so a hostname that resolves to a private address is caught too.
func (c *Client) checkAddress(_, address string, _ syscall.RawConn) error {
	if c.allowPrivate {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !isPublic(ip) {
		return ErrPrivateAddress
	}
	return nil
}

var nonPublic = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4",
		"64:ff9b::/96", "2001:db8::/32",
	} {
		_, n, _ := net.ParseCIDR(cidr)
		out = append(out, n)
	}
	return out
}()

func isPublic(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, n := range nonPublic {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// CheckURL validates a URL a user typed and returns it normalized.
func CheckURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	switch {
	case raw == "" || err != nil || u.Host == "":
		return "", &sources.InvalidSearch{Msg: "enter the page's address, e.g. https://shop.example/product"}
	case u.Scheme != "http" && u.Scheme != "https":
		return "", &sources.InvalidSearch{Msg: "only http and https pages can be watched"}
	case u.User != nil:
		return "", &sources.InvalidSearch{Msg: "addresses with a user name or password are not supported"}
	case len(raw) > maxURLLen:
		return "", &sources.InvalidSearch{Msg: "that address is too long"}
	}
	u.Fragment = ""
	return u.String(), nil
}

// StatusError is an HTTP error the site answered with.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s answered %d", hostOf(e.URL), e.Code) }

// HTTPStatus lets callers tell the site pushing back from other errors.
func (e *StatusError) HTTPStatus() int { return e.Code }

// Unwrap classifies the status for callers (errors.Is).
func (e *StatusError) Unwrap() error {
	switch e.Code {
	case http.StatusNotFound, http.StatusGone:
		return sources.ErrPageGone
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return sources.ErrBlocked
	}
	return nil
}

// Fetch GETs a page. Text is decoded to UTF-8 from whatever charset the
// page declares.
func (c *Client) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	target, err := CheckURL(rawURL)
	if err != nil {
		return nil, err
	}
	if err := c.throttle(ctx, hostOf(target)); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en,lv;q=0.8")

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrPrivateAddress) {
			return nil, &sources.InvalidSearch{Msg: ErrPrivateAddress.Error()}
		}
		return nil, fmt.Errorf("fetch %s: %w", hostOf(target), err)
	}
	defer func() { _ = resp.Body.Close() }()
	final := resp.Request.URL.String()
	if resp.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil, &StatusError{URL: final, Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", hostOf(final), err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%s: the page is larger than %d MB", hostOf(final), maxBody>>20)
	}

	header := resp.Header.Get("Content-Type")
	media, _, _ := mime.ParseMediaType(header)
	if media == "" {
		media = http.DetectContentType(body)
		media, _, _ = mime.ParseMediaType(media)
	}
	p := &Page{URL: final, Status: resp.StatusCode, ContentType: media, FetchedAt: time.Now()}
	if !strings.HasPrefix(media, "text/") && media != "application/xhtml+xml" && !strings.Contains(media, "json") {
		return nil, fmt.Errorf("%s is %s, not a web page or JSON", hostOf(final), media)
	}
	if strings.Contains(media, "json") {
		p.Body = body
		return p, nil
	}
	// Pages may be in any charset (ss.com is windows-1257): convert, using
	// the header or the page's own <meta charset>.
	r, err := charset.NewReader(bytes.NewReader(body), header)
	if err != nil {
		p.Body = body
		return p, nil
	}
	if p.Body, err = io.ReadAll(r); err != nil {
		p.Body = body
	}
	return p, nil
}

// throttle waits until gap has passed since the previous request to host.
func (c *Client) throttle(ctx context.Context, host string) error {
	if c.gap <= 0 {
		return nil
	}
	c.mu.Lock()
	wait := c.gap - time.Since(c.last[host])
	c.last[host] = time.Now().Add(max(wait, 0))
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}
