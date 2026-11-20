package pages

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

func TestParsePrice(t *testing.T) {
	cases := []struct {
		raw, fallback string
		machine       bool
		cents         int64
		currency      string
	}{
		{"1 299 €", "", false, 129900, "EUR"},
		{" 1 299   €", "", false, 129900, "EUR"},
		{"1 073,55 €", "", false, 107355, "EUR"},
		{"$1,299.99", "", false, 129999, "USD"},
		{"1.299,00 EUR", "", false, 129900, "EUR"},
		{"1.299 €", "", false, 129900, "EUR"},       // one dot + 3 digits = grouping
		{"12,5 €", "", false, 1250, "EUR"},          // one comma + 1 digit = decimals
		{"1299,-", "SEK", false, 129900, "SEK"},     // Nordic whole units
		{"1 299 kr", "NOK", false, 129900, "NOK"},   // "kr" settled by the fallback
		{"1 299 kr", "EUR", false, 129900, "EUR"},   // not Nordic: keep the fallback
		{"£24.99 inc. VAT", "", false, 2499, "GBP"}, //
		{"CHF 1'299.50", "", false, 129950, "CHF"},
		{"1299", "EUR", true, 129900, "EUR"},
		{"1073.55", "EUR", true, 107355, "EUR"},
		{"1.299", "EUR", true, 130, "EUR"}, // a program's 1.299 is one point three
		{"29.999", "", true, 3000, ""},
	}
	for _, c := range cases {
		p, err := ParsePrice(c.raw, c.fallback, c.machine)
		if err != nil {
			t.Errorf("%q: %v", c.raw, err)
			continue
		}
		if p.Cents != c.cents || p.Currency != c.currency {
			t.Errorf("%q = %d %s, want %d %s", c.raw, p.Cents, p.Currency, c.cents, c.currency)
		}
	}
	for _, bad := range []string{"", "Including tax", "Sold out"} {
		if _, err := ParsePrice(bad, "EUR", false); !errors.Is(err, sources.ErrNoPrice) {
			t.Errorf("%q: got %v, want ErrNoPrice", bad, err)
		}
	}
}

func TestFormatCents(t *testing.T) {
	for cents, want := range map[int64]string{129900: "€1,299", 107355: "€1,073.55", 99: "€0.99", 12345678: "€123,456.78"} {
		if got := sources.FormatCents(cents, "EUR"); got != want {
			t.Errorf("%d = %q, want %q", cents, got, want)
		}
	}
	if got := sources.FormatCents(129900, "SEK"); got != "1,299 SEK" {
		t.Errorf("SEK = %q", got)
	}
}

func page(t *testing.T, file, contentType string) *Page {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return &Page{URL: "https://shop.example/product/1", Status: 200, ContentType: contentType, Body: b}
}

func TestExtract(t *testing.T) {
	sb := page(t, "strandberg.html", "text/html")
	jl := page(t, "jsonld.html", "text/html")
	api := page(t, "api.json", "application/json")
	cases := []struct {
		name  string
		p     *Page
		x     sources.Extractor
		raw   string
		cents int64
		cur   string
	}{
		// What the picker produces for the visible price: React's <!-- -->
		// markers and odd spacing must not matter.
		{"visible price", sb, sources.Extractor{Selector: "div.product p > span.text-xl"}, "1 299 €", 129900, "EUR"},
		{"hidden field", sb, sources.Extractor{Selector: `input[name="cartItemPriceDisplayIncVat"]`, Attr: "value", Currency: "EUR"}, "1299", 129900, "EUR"},
		{"json-ld in the second script", jl, sources.Extractor{Selector: `script[type="application/ld+json"]`, JSONPath: "@graph[1].offers.price", Currency: "EUR"}, "399.00", 39900, "EUR"},
		{"meta tag", jl, sources.Extractor{Selector: `meta[property="product:price:amount"]`, Attr: "content", Currency: "EUR"}, "399.00", 39900, "EUR"},
		{"api", api, sources.Extractor{JSONPath: "data.product.variants[1].price.amount", Currency: "EUR"}, "54.90", 5490, "EUR"},
		{"api number", api, sources.Extractor{JSONPath: "data.product.listPrice", Currency: "EUR"}, "34.99", 3499, "EUR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Extract(c.p, c.x)
			if err != nil {
				t.Fatal(err)
			}
			if res.Raw != c.raw || res.Price.Cents != c.cents || res.Price.Currency != c.cur {
				t.Errorf("got %q = %d %s, want %q = %d %s", res.Raw, res.Price.Cents, res.Price.Currency, c.raw, c.cents, c.cur)
			}
		})
	}

	for name, x := range map[string]sources.Extractor{
		"gone element":  {Selector: "span.sale-price"},
		"text no price": {Selector: "span.italic"},
		"missing key":   {JSONPath: "data.product.price"},
		"index too far": {JSONPath: "data.product.variants[5].price.amount"},
		"not a number":  {JSONPath: "data.product.name"},
	} {
		p := sb
		if x.Selector == "" {
			p = api
		}
		if _, err := Extract(p, x); !errors.Is(err, sources.ErrNoPrice) {
			t.Errorf("%s: got %v, want ErrNoPrice", name, err)
		}
	}
	var inv *sources.InvalidSearch
	if _, err := Extract(sb, sources.Extractor{Selector: "span[["}); !errors.As(err, &inv) {
		t.Errorf("bad selector: got %v, want InvalidSearch", err)
	}
	if _, err := Extract(api, sources.Extractor{JSONPath: "data..x"}); !errors.As(err, &inv) {
		t.Errorf("bad path: got %v, want InvalidSearch", err)
	}
}

func TestSuggest(t *testing.T) {
	got := map[string]sources.Price{}
	for _, s := range Suggest(page(t, "strandberg.html", "text/html")) {
		got[s.Label] = s.Price
	}
	// No meta currency there: the € in the page's text settles it. The
	// price list's ID is a number named like a price, not a price.
	if got["cartItemPriceDisplayIncVat"] != (sources.Price{Cents: 129900, Currency: "EUR"}) ||
		got["cartItemPriceDisplay"] != (sources.Price{Cents: 107355, Currency: "EUR"}) || len(got) != 2 {
		t.Errorf("strandberg: %v", got)
	}

	ss := Suggest(page(t, "jsonld.html", "text/html"))
	if len(ss) == 0 || ss[0].Kind != "structured" || ss[0].Label != "@graph[1].offers.price" || ss[0].Price != (sources.Price{Cents: 39900, Currency: "EUR"}) {
		t.Fatalf("json-ld first: %+v", ss)
	}
	labels := map[string]bool{}
	for _, s := range ss {
		labels[s.Label] = true
	}
	for _, want := range []string{"product:price:amount", "itemprop=price", "span.product-price", "span.old-price"} {
		if !labels[want] {
			t.Errorf("missing suggestion %s in %v", want, labels)
		}
	}

	api := Suggest(page(t, "api.json", "application/json"))
	paths := map[string]bool{}
	for _, s := range api {
		paths[s.Label] = true
		if s.Price.Currency != "EUR" {
			t.Errorf("%s: currency %q", s.Label, s.Price.Currency)
		}
	}
	for _, want := range []string{"data.product.listPrice", "data.product.variants[0].price.amount"} {
		if !paths[want] {
			t.Errorf("missing %s in %v", want, paths)
		}
	}
}

func TestAbout(t *testing.T) {
	title, image := About(page(t, "strandberg.html", "text/html"))
	if title != "Boden Essential 6 Tremolo Whale Blue" || image != "https://shop.example/images/boden-essential-6-whale-blue.jpg" {
		t.Errorf("got %q %q", title, image)
	}
}

func TestPickerHTML(t *testing.T) {
	out, err := PickerHTML(page(t, "strandberg.html", "text/html"), "N0NCE")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{"main.js", "__next_f", "onclick", "javascript:", "youtube", `rel="preload"`} {
		if strings.Contains(s, gone) {
			t.Errorf("copy still has %q", gone)
		}
	}
	for _, kept := range []string{
		`<base href="https://shop.example/product/1"/>`, `href="/_next/static/css/app.css"`,
		`<script nonce="N0NCE">`, "x-price-picker", `src="/images/gallery-1.jpg"`, "1 299",
	} {
		if !strings.Contains(s, kept) {
			t.Errorf("copy lacks %q", kept)
		}
	}
	if strings.Count(strings.ToLower(s), "<!doctype") != 1 {
		t.Error("want exactly one doctype")
	}
	if strings.Count(s, "<script") != 1 {
		t.Errorf("want only the picker's script, got %d", strings.Count(s, "<script"))
	}
}

// Streaming renders park the real content in hidden divs that scripts move
// into place; the copy and the reader both put it where the scripts would.
func TestStreamedRender(t *testing.T) {
	p := page(t, "streamed.html", "text/html")
	out, err := PickerHTML(p, "N")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{"Loading…", `hidden=""`, `id="S:0"`, `id="B:0"`, `id="P:3"`} {
		if strings.Contains(s, gone) {
			t.Errorf("copy still has %q", gone)
		}
	}
	if !strings.Contains(s, `<section class="product-info"><!--$--><div class="price-row">`) ||
		!strings.Contains(s, `<aside class="related"><a class="related-item">`) {
		t.Errorf("content not in place:\n%s", s)
	}
	// A selector the picker builds on the revealed page reads on the server.
	res, err := Extract(p, sources.Extractor{Selector: "section.product-info > div.price-row span.text-xl"})
	if err != nil || res.Price != (sources.Price{Cents: 129900, Currency: "EUR"}) {
		t.Errorf("extract: %+v %v", res, err)
	}
}

func TestFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>secret</p>"))
	}))
	defer srv.Close()
	ctx := context.Background()

	var inv *sources.InvalidSearch
	if _, err := New(5*time.Second, 0).Fetch(ctx, srv.URL); !errors.As(err, &inv) || !strings.Contains(err.Error(), "private") {
		t.Fatalf("loopback: got %v", err)
	}
	if _, err := New(5*time.Second, 0).AllowPrivate().Fetch(ctx, srv.URL); err != nil {
		t.Fatalf("allowed: %v", err)
	}
	for _, ip := range []string{"10.0.0.1", "172.17.0.2", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "0.0.0.0"} {
		if isPublic(net.ParseIP(ip)) {
			t.Errorf("%s counted as public", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "185.15.59.224", "2606:4700::1111"} {
		if !isPublic(net.ParseIP(ip)) {
			t.Errorf("%s counted as private", ip)
		}
	}
	for _, bad := range []string{"", "ftp://x.example/a", "file:///etc/passwd", "https://user:pw@x.example/"} {
		if _, err := CheckURL(bad); !errors.As(err, &inv) {
			t.Errorf("%q accepted", bad)
		}
	}
	if u, _ := CheckURL("shop.example/p#reviews"); u != "https://shop.example/p" {
		t.Errorf("normalized to %q", u)
	}
}

func TestReadPrice(t *testing.T) {
	body, _ := os.ReadFile("testdata/strandberg.html")
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/product", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := New(5*time.Second, 0).AllowPrivate()
	x := sources.Extractor{Selector: "div.product p > span.text-xl"}

	r, err := c.ReadPrice(context.Background(), srv.URL+"/moved", x)
	if err != nil {
		t.Fatal(err)
	}
	if r.Price != (sources.Price{Cents: 129900, Currency: "EUR"}) || r.Title != "Boden Essential 6 Tremolo Whale Blue" {
		t.Errorf("got %+v", r)
	}

	for code, want := range map[int]error{404: sources.ErrPageGone, 410: sources.ErrPageGone, 403: sources.ErrBlocked, 429: sources.ErrBlocked} {
		status = code
		if _, err := c.ReadPrice(context.Background(), srv.URL, x); !errors.Is(err, want) {
			t.Errorf("%d: got %v, want %v", code, err, want)
		}
	}
	status = http.StatusOK
	if _, err := c.ReadPrice(context.Background(), srv.URL, sources.Extractor{Selector: "span.gone"}); !errors.Is(err, sources.ErrNoPrice) {
		t.Errorf("layout change: got %v", err)
	}
}

// PAGES_LIVE=1 go test ./internal/scraping/pages -run TestLive reads the
// Strandberg page the feature was designed on (one real request).
func TestLiveStrandberg(t *testing.T) {
	if os.Getenv("PAGES_LIVE") == "" {
		t.Skip("set PAGES_LIVE=1 to read the live page")
	}
	c := New(30*time.Second, 0)
	url := "https://strandbergguitars.com/en-LV/product/boden-essential-6-tremolo-whale-blue"
	p, err := c.Fetch(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []sources.Extractor{
		{Selector: `input[name="cartItemPriceDisplayIncVat"]`, Attr: "value", Currency: "EUR"},
		{Selector: "p > span.text-xl"},
	} {
		res, err := Extract(p, x)
		if err != nil {
			t.Errorf("%+v: %v", x, err)
			continue
		}
		t.Logf("%s: %q = %s (%d matches)", x.Selector, res.Raw, res.Price, res.Matches)
		if res.Price.Currency != "EUR" || res.Price.Cents < 10000 {
			t.Errorf("%+v: implausible %v", x, res.Price)
		}
	}
	for _, s := range Suggest(p) {
		t.Logf("suggestion %s %s: %q = %s", s.Kind, s.Label, s.Raw, s.Price)
	}
}
