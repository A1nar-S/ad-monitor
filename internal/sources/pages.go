package sources

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Price watches follow one product page (or API endpoint) that the user
// picked, not a feed. Where the price is on the page is an Extractor, chosen
// once in the dashboard's element picker; a PageReader reads it.

// Extractor says where a price is in a fetched page.
type Extractor struct {
	// Selector is a CSS selector; the first element it matches holds the
	// price. Empty means the whole response (a JSON API).
	Selector string `json:"selector,omitempty"`
	// Attr reads this attribute of the element instead of its text (a meta
	// tag's content, a hidden input's value).
	Attr string `json:"attr,omitempty"`
	// JSONPath walks the value as JSON (an API response, a JSON-LD script),
	// e.g. "offers.price" or "data.items[0].price".
	JSONPath string `json:"jsonPath,omitempty"`
	// Currency applies when the value shows none ("1299" rather than "1 299 €").
	Currency string `json:"currency,omitempty"`
}

// Validate rejects extractors no page could satisfy.
func (x Extractor) Validate() error {
	switch {
	case x.Selector == "" && x.JSONPath == "":
		return &InvalidSearch{Msg: "pick the element with the price"}
	case len(x.Selector) > 500 || len(x.JSONPath) > 200 || len(x.Attr) > 60:
		return &InvalidSearch{Msg: "the price's location is too long"}
	case x.Currency != "" && !isCurrencyCode(x.Currency):
		return &InvalidSearch{Msg: fmt.Sprintf("%q is not a currency code like EUR", x.Currency)}
	}
	for _, r := range x.Attr {
		if r != '-' && r != '_' && r != ':' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return &InvalidSearch{Msg: fmt.Sprintf("%q is not an attribute name", x.Attr)}
		}
	}
	return nil
}

func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// Price is an exact amount: Cents are hundredths of the currency's unit,
// whatever the currency (no float rounding anywhere).
type Price struct {
	Cents    int64  `json:"cents"`
	Currency string `json:"currency"` // ISO 4217, e.g. "EUR"; empty if unknown
}

var currencySymbols = map[string]string{"EUR": "€", "USD": "$", "GBP": "£", "JPY": "¥", "INR": "₹"}

// String writes the price for a chat message: "€1,299", "€1,073.55",
// "1,299 SEK".
func (p Price) String() string { return FormatCents(p.Cents, p.Currency) }

// FormatCents writes an amount in a currency (see Price.String).
func FormatCents(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if frac := cents % 100; frac != 0 {
		fmt.Fprintf(&b, ".%02d", frac)
	}
	if sym, ok := currencySymbols[currency]; ok {
		return sign + sym + b.String()
	}
	if currency == "" {
		return sign + b.String()
	}
	return sign + b.String() + " " + currency
}

// Reading is what a page said when it was read.
type Reading struct {
	Price Price
	Raw   string // the text or value the price was parsed from, trimmed
	// About the page, for showing the watch.
	Title    string
	ImageURL string
}

// PageReader reads a price from a page.
type PageReader interface {
	ReadPrice(ctx context.Context, url string, x Extractor) (Reading, error)
}

// Why a page could not be read. Readers wrap these, so callers can tell a
// removed product from a changed layout or a site turning the reader away.
var (
	// ErrPageGone: the page answers 404 or 410.
	ErrPageGone = errors.New("the page no longer exists")
	// ErrBlocked: the site refuses (401, 403, 429). Never worked around.
	ErrBlocked = errors.New("the site refused the request")
	// ErrNoPrice: the page loaded, but the picked element is missing or
	// holds no price; usually the layout changed.
	ErrNoPrice = errors.New("no price where it used to be")
)
