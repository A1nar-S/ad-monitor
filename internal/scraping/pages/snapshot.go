package pages

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// The element picker shows the user a copy of the page as this server read
// it, so what they click is what the watch will read later. The copy has no
// scripts of its own (only the picker's), runs in a sandboxed iframe with an
// opaque origin and a strict CSP, and loads the shop's styles and images
// from the shop itself.

//go:embed picker.js
var pickerScript string

// PickerCSP is the Content-Security-Policy the picker copy must be served
// with; nonce is the one passed to PickerHTML. "sandbox" keeps the copy in
// an opaque origin even when opened directly.
func PickerCSP(nonce string) string {
	return "sandbox allow-scripts; default-src 'none'; script-src 'nonce-" + nonce + "'; " +
		"style-src * data: 'unsafe-inline'; img-src * data: blob:; font-src * data:; media-src *; " +
		"form-action 'none'; frame-ancestors 'self'"
}

// removed are elements that could run code, load other documents or move
// the page, plus noscript (scripts run in the picker, so browsers hide it).
var removed = "script, noscript, iframe, frame, frameset, object, embed, applet, portal, base, " +
	`meta[http-equiv], link:not([rel~="stylesheet"]):not([rel~="icon"])`

// PickerHTML returns the page, made safe to show, with the picker added.
func PickerHTML(p *Page, nonce string) ([]byte, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(p.Body))
	if err != nil {
		return nil, err
	}
	revealStreamed(doc)
	doc.Find(removed).Remove()
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		n := s.Get(0)
		kept := n.Attr[:0]
		for _, a := range n.Attr {
			key := strings.ToLower(a.Key)
			if strings.HasPrefix(key, "on") || key == "srcdoc" || key == "formaction" ||
				isURLAttr(key) && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
				continue
			}
			kept = append(kept, a)
		}
		n.Attr = kept
	})
	// Lazy images keep the real address in data-src until a script swaps it.
	doc.Find("img[data-src], img[data-srcset], source[data-srcset]").Each(func(_ int, s *goquery.Selection) {
		if v := s.AttrOr("data-src", ""); v != "" && (s.AttrOr("src", "") == "" || strings.HasPrefix(s.AttrOr("src", ""), "data:")) {
			s.SetAttr("src", v)
		}
		if v := s.AttrOr("data-srcset", ""); v != "" && s.AttrOr("srcset", "") == "" {
			s.SetAttr("srcset", v)
		}
	})

	head := doc.Find("head")
	if head.Length() == 0 {
		doc.Find("html").PrependHtml("<head></head>")
		head = doc.Find("head")
	}
	head.PrependNodes(
		element("meta", "charset", "utf-8"),
		element("base", "href", p.URL),
		element("meta", "name", "referrer", "content", "no-referrer"),
	)
	script := element("script", "nonce", nonce)
	script.AppendChild(&html.Node{Type: html.TextNode, Data: pickerScript})
	doc.Find("body").AppendNodes(script)

	// One doctype, ours: standards mode even for pages that had none.
	root := doc.Get(0)
	for c := root.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.DoctypeNode {
			root.RemoveChild(c)
		}
		c = next
	}
	out, err := goquery.OuterHtml(doc.Selection)
	if err != nil {
		return nil, err
	}
	return []byte("<!DOCTYPE html>" + out), nil
}

// streamCall is one of React's streaming-render instructions: $RC("B:1",
// "S:1") swaps a loading skeleton for its content, $RS("S:5","P:5") fills a
// placeholder.
var streamCall = regexp.MustCompile(`\$R([CS])\("([^"]+)","([^"]+)"`)

// revealStreamed does what React's inline scripts would. Pages rendered with
// streaming (Next.js and others) send a skeleton first and the real content
// later in <div hidden id="S:…">; a script moves it into place. The picker's
// copy has no scripts, so without this the price would sit in a hidden div.
func revealStreamed(doc *goquery.Document) {
	var calls [][]string
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		calls = append(calls, streamCall.FindAllStringSubmatch(s.Text(), -1)...)
	})
	byID := func(id string) *html.Node {
		return doc.FindMatcher(goquery.Single(`[id="` + strings.ReplaceAll(id, `"`, "") + `"]`)).Get(0)
	}
	for _, c := range calls {
		switch c[1] {
		case "C": // $RC(boundary, content)
			boundary, content := byID(c[2]), byID(c[3])
			if boundary == nil || content == nil || boundary.Parent == nil {
				continue
			}
			parent := boundary.Parent
			// The skeleton runs from the template to the boundary's end
			// marker <!--/$-->, past any nested boundaries.
			var end *html.Node
			depth := 0
			for n := boundary.NextSibling; n != nil && end == nil; {
				next := n.NextSibling
				if n.Type == html.CommentNode {
					switch {
					case n.Data == "/$" && depth == 0:
						end = n
						continue
					case n.Data == "/$":
						depth--
					case strings.HasPrefix(n.Data, "$"):
						depth++
					}
				}
				parent.RemoveChild(n)
				n = next
			}
			if end == nil {
				continue
			}
			moveChildren(content, parent, end)
			if prev := boundary.PrevSibling; prev != nil && prev.Type == html.CommentNode && prev.Data == "$?" {
				prev.Data = "$"
			}
			parent.RemoveChild(boundary)
			content.Parent.RemoveChild(content)
		case "S": // $RS(content, placeholder)
			content, placeholder := byID(c[2]), byID(c[3])
			if content == nil || placeholder == nil || placeholder.Parent == nil {
				continue
			}
			moveChildren(content, placeholder.Parent, placeholder)
			placeholder.Parent.RemoveChild(placeholder)
			content.Parent.RemoveChild(content)
		}
	}
}

// moveChildren moves from's children before before.
func moveChildren(from, parent, before *html.Node) {
	for c := from.FirstChild; c != nil; {
		next := c.NextSibling
		from.RemoveChild(c)
		parent.InsertBefore(c, before)
		c = next
	}
}

func isURLAttr(key string) bool {
	switch key {
	case "href", "src", "action", "xlink:href", "data", "poster", "background":
		return true
	}
	return false
}

func element(tag string, attrs ...string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.Attr = append(n.Attr, html.Attribute{Key: attrs[i], Val: attrs[i+1]})
	}
	return n
}

// Snapshots keeps recently inspected pages for a short while, so the
// picker, the "test" call and saving a watch all see the same copy without
// fetching the shop again.
type Snapshots struct {
	ttl time.Duration
	max int

	mu sync.Mutex
	m  map[string]snapshot
}

type snapshot struct {
	page    *Page
	owner   int64
	expires time.Time
}

func NewSnapshots(ttl time.Duration, max int) *Snapshots {
	return &Snapshots{ttl: ttl, max: max, m: map[string]snapshot{}}
}

// Put stores a page for owner and returns its id, an unguessable token:
// the picker's iframe loads the copy by id alone, without the session.
func (s *Snapshots) Put(owner int64, p *Page) string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	for len(s.m) >= s.max {
		var oldest string
		for k, v := range s.m {
			if oldest == "" || v.expires.Before(s.m[oldest].expires) {
				oldest = k
			}
		}
		delete(s.m, oldest)
	}
	s.m[id] = snapshot{page: p, owner: owner, expires: now.Add(s.ttl)}
	return id
}

// Get returns a stored page and who stored it.
func (s *Snapshots) Get(id string) (*Page, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok || time.Now().After(v.expires) {
		return nil, 0, false
	}
	return v.page, v.owner, true
}

// Nonce returns a random value for a CSP nonce.
func Nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}
