package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Result is what an extractor found on a page.
type Result struct {
	Raw     string        // trimmed text or value
	Price   sources.Price // parsed from Raw
	Matches int           // elements the selector matched (0 for JSON)
}

// Extract applies x to a fetched page. The selector's first match that
// yields a value wins; with JSONPath, each match is tried until the path
// resolves (a page can have several JSON-LD scripts).
func Extract(p *Page, x sources.Extractor) (Result, error) {
	if err := x.Validate(); err != nil {
		return Result{}, err
	}
	machine := x.Attr != "" || x.JSONPath != ""
	if x.Selector == "" {
		if !p.IsJSON() {
			return Result{}, fmt.Errorf("%w: the page is not JSON", sources.ErrNoPrice)
		}
		raw, err := jsonValue(p.Body, x.JSONPath)
		if err != nil {
			return Result{}, err
		}
		return parsed(raw, x.Currency, machine, 0)
	}

	sel, err := cascadia.Compile(x.Selector)
	if err != nil {
		return Result{}, &sources.InvalidSearch{Msg: fmt.Sprintf("%q is not a CSS selector the reader understands", x.Selector)}
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(p.Body))
	if err != nil {
		return Result{}, fmt.Errorf("parse page: %w", err)
	}
	// The same document the picker showed: streamed content in place.
	revealStreamed(doc)
	found := doc.FindMatcher(sel)
	if found.Length() == 0 {
		return Result{}, fmt.Errorf("%w: nothing on the page matches %s", sources.ErrNoPrice, x.Selector)
	}
	var lastErr error
	for i := range found.Length() {
		el := found.Eq(i)
		raw := elementValue(el, x.Attr)
		if x.JSONPath != "" {
			if raw, lastErr = jsonValue([]byte(raw), x.JSONPath); lastErr != nil {
				continue
			}
		}
		if raw == "" {
			lastErr = fmt.Errorf("%w: the element is empty", sources.ErrNoPrice)
			continue
		}
		return parsed(raw, x.Currency, machine, found.Length())
	}
	return Result{Matches: found.Length()}, lastErr
}

func parsed(raw, currency string, machine bool, matches int) (Result, error) {
	price, err := ParsePrice(raw, currency, machine)
	return Result{Raw: clip(raw, 200), Price: price, Matches: matches}, err
}

// elementValue is an element's text with whitespace collapsed, or one of
// its attributes.
func elementValue(el *goquery.Selection, attr string) string {
	if attr != "" {
		v, _ := el.Attr(attr)
		return strings.TrimSpace(v)
	}
	if goquery.NodeName(el) == "script" {
		return strings.TrimSpace(el.Text())
	}
	return strings.Join(strings.Fields(el.Text()), " ")
}

// jsonValue walks path through a JSON document and returns the number or
// string found there.
func jsonValue(body []byte, path string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("%w: not valid JSON", sources.ErrNoPrice)
	}
	got, err := walk(v, path)
	if err != nil {
		return "", err
	}
	switch t := got.(type) {
	case json.Number:
		return t.String(), nil
	case string:
		return strings.TrimSpace(t), nil
	case nil:
		return "", fmt.Errorf("%w: %s is empty", sources.ErrNoPrice, path)
	}
	return "", fmt.Errorf("%w: %s is not a number", sources.ErrNoPrice, path)
}

// walk follows a path like "data.items[0].price" or "@graph[1].offers.price".
func walk(v any, path string) (any, error) {
	steps, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	for i, s := range steps {
		where := joinPath(steps[:i+1])
		switch {
		case s.key != "":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: %s is not an object", sources.ErrNoPrice, joinPath(steps[:i]))
			}
			if v, ok = m[s.key]; !ok {
				return nil, fmt.Errorf("%w: %s is missing", sources.ErrNoPrice, where)
			}
		default:
			a, ok := v.([]any)
			if !ok || s.index >= len(a) {
				return nil, fmt.Errorf("%w: %s is missing", sources.ErrNoPrice, where)
			}
			v = a[s.index]
		}
	}
	return v, nil
}

type step struct {
	key   string
	index int
}

func parsePath(path string) ([]step, error) {
	var out []step
	bad := &sources.InvalidSearch{Msg: fmt.Sprintf("%q is not a JSON path like data.items[0].price", path)}
	for _, part := range strings.Split(path, ".") {
		key, rest, _ := strings.Cut(part, "[")
		if key == "" && rest == "" {
			return nil, bad
		}
		if key != "" {
			out = append(out, step{key: key})
		}
		if rest == "" {
			continue
		}
		for _, idx := range strings.Split("["+rest, "[")[1:] {
			n, err := strconv.Atoi(strings.TrimSuffix(idx, "]"))
			if err != nil || !strings.HasSuffix(idx, "]") || n < 0 {
				return nil, bad
			}
			out = append(out, step{index: n})
		}
	}
	return out, nil
}

func joinPath(steps []step) string {
	var b strings.Builder
	for _, s := range steps {
		if s.key != "" {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.key)
		} else {
			fmt.Fprintf(&b, "[%d]", s.index)
		}
	}
	if b.Len() == 0 {
		return "the document"
	}
	return b.String()
}

// About is a page's title and picture, for showing a watch.
func About(p *Page) (title, image string) {
	if p.IsJSON() {
		return "", ""
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(p.Body))
	if err != nil {
		return "", ""
	}
	revealStreamed(doc)
	// A product page's one heading is the product's name; og:title is often
	// just the shop's.
	if h := doc.Find("h1"); h.Length() == 1 {
		if v := strings.Join(strings.Fields(h.Text()), " "); len(v) >= 3 && len(v) <= 150 {
			title = v
		}
	}
	for _, sel := range []string{`meta[property="og:title"]`, `meta[name="twitter:title"]`} {
		if v, _ := doc.Find(sel).First().Attr("content"); title == "" && strings.TrimSpace(v) != "" {
			title = v
			break
		}
	}
	if title == "" {
		title = doc.Find("title").First().Text()
	}
	if v, _ := doc.Find(`meta[property="og:image"]`).First().Attr("content"); v != "" {
		image = resolve(p.URL, v)
	}
	return clip(strings.Join(strings.Fields(title), " "), 200), image
}

func resolve(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	r, err := b.Parse(strings.TrimSpace(ref))
	if err != nil || (r.Scheme != "http" && r.Scheme != "https") {
		return ""
	}
	return r.String()
}

// ReadPrice fetches a page and reads its price (sources.PageReader).
func (c *Client) ReadPrice(ctx context.Context, rawURL string, x sources.Extractor) (sources.Reading, error) {
	p, err := c.Fetch(ctx, rawURL)
	if err != nil {
		return sources.Reading{}, err
	}
	res, err := Extract(p, x)
	if err != nil {
		var inv *sources.InvalidSearch
		if errors.As(err, &inv) {
			// A bad selector or path is the watch's problem, not the page's.
			return sources.Reading{}, fmt.Errorf("%w: %s", sources.ErrNoPrice, inv.Msg)
		}
		return sources.Reading{Raw: res.Raw}, err
	}
	title, image := About(p)
	return sources.Reading{Price: res.Price, Raw: res.Raw, Title: title, ImageURL: image}, nil
}
