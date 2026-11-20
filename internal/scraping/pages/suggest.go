package pages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Suggestion is a place a price was found without the user pointing at it.
type Suggestion struct {
	Kind      string            `json:"kind"`  // structured, meta, field, element, json
	Label     string            `json:"label"` // what it is: "offers.price", "cartItemPriceDisplayIncVat"
	Extractor sources.Extractor `json:"extractor"`
	Raw       string            `json:"raw"`
	Price     sources.Price     `json:"price"`
}

const maxSuggestions = 8

// Suggest finds likely prices: schema.org data, shop meta tags, form fields
// and elements named like prices, or JSON keys named like prices. Every
// suggestion has been read back through Extract, so picking one works.
func Suggest(p *Page) []Suggestion {
	var out []Suggestion
	add := func(kind, label string, x sources.Extractor) {
		if len(out) >= maxSuggestions {
			return
		}
		res, err := Extract(p, x)
		if err != nil || res.Price.Cents == 0 {
			return
		}
		for _, s := range out {
			if s.Extractor == x {
				return
			}
		}
		out = append(out, Suggestion{Kind: kind, Label: label, Extractor: x, Raw: res.Raw, Price: res.Price})
	}

	if p.IsJSON() {
		var v any
		dec := json.NewDecoder(bytes.NewReader(p.Body))
		dec.UseNumber()
		if dec.Decode(&v) == nil {
			for _, f := range priceKeys(v, "", 0) {
				add("json", f.path, sources.Extractor{JSONPath: f.path, Currency: f.currency})
			}
		}
		return out
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(p.Body))
	if err != nil {
		return nil
	}
	pageCurrency := GuessCurrency(p)

	// schema.org Product data: the most deliberate statement of a price.
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		var v any
		dec := json.NewDecoder(strings.NewReader(s.Text()))
		dec.UseNumber()
		if dec.Decode(&v) != nil {
			return
		}
		for _, f := range offerPrices(v, "", 0) {
			add("structured", f.path, sources.Extractor{
				Selector: `script[type="application/ld+json"]`, JSONPath: f.path, Currency: f.currency,
			})
		}
	})

	for _, m := range []string{"product:price:amount", "og:price:amount"} {
		add("meta", m, sources.Extractor{Selector: fmt.Sprintf(`meta[property=%q]`, m), Attr: "content", Currency: pageCurrency})
	}
	doc.Find(`[itemprop="price"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		x := sources.Extractor{Selector: `[itemprop="price"]`, Currency: pageCurrency}
		if _, ok := s.Attr("content"); ok {
			x.Attr = "content"
		}
		add("meta", "itemprop=price", x)
		return false
	})

	// Elements whose class says price, with a short text holding a number.
	doc.Find(`[class*="price" i]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		text := strings.Join(strings.Fields(s.Text()), " ")
		if text == "" || len(text) > 40 || s.Children().Length() > 3 {
			return true
		}
		for _, c := range strings.Fields(s.AttrOr("class", "")) {
			if strings.Contains(strings.ToLower(c), "price") && plainIdent(c) {
				add("element", goquery.NodeName(s)+"."+c, sources.Extractor{Selector: goquery.NodeName(s) + "." + c, Currency: pageCurrency})
				break
			}
		}
		return len(out) < maxSuggestions
	})

	// Hidden form fields named like prices (cart forms carry the exact amount).
	doc.Find(`input[name*="price" i][value]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		name := s.AttrOr("name", "")
		if plainIdent(name) && !strings.HasSuffix(strings.ToLower(name), "id") {
			add("field", name, sources.Extractor{Selector: fmt.Sprintf(`input[name=%q]`, name), Attr: "value", Currency: pageCurrency})
		}
		return len(out) < maxSuggestions
	})
	return out
}

// plainIdent: a name usable in a selector without escaping.
func plainIdent(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		if r != '-' && r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func metaCurrency(doc *goquery.Document) string {
	for _, sel := range []string{
		`meta[property="product:price:currency"]`, `meta[property="og:price:currency"]`, `[itemprop="priceCurrency"]`,
	} {
		s := doc.Find(sel).First()
		v := strings.ToUpper(strings.TrimSpace(s.AttrOr("content", s.Text())))
		if isoCodes[v] {
			return v
		}
	}
	return ""
}

type found struct{ path, currency string }

// offerPrices finds schema.org offers' prices in a JSON-LD document: in a
// Product's offers, an Offer or AggregateOffer, an @graph, or a list.
func offerPrices(v any, path string, depth int) []found {
	if depth > 6 {
		return nil
	}
	var out []found
	switch t := v.(type) {
	case []any:
		for i, e := range t {
			out = append(out, offerPrices(e, fmt.Sprintf("%s[%d]", path, i), depth+1)...)
		}
	case map[string]any:
		cur, _ := t["priceCurrency"].(string)
		for _, key := range []string{"price", "lowPrice"} {
			if isNumberish(t[key]) && (isType(t, "Offer") || isType(t, "AggregateOffer") || cur != "") {
				out = append(out, found{join(path, key), strings.ToUpper(cur)})
				break
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if k == "offers" || k == "@graph" || k == "mainEntity" || k == "hasVariant" || k == "priceSpecification" {
				out = append(out, offerPrices(t[k], join(path, k), depth+1)...)
			}
		}
	}
	return out
}

// priceKeys finds keys named like prices in a JSON API response.
func priceKeys(v any, path string, depth int) []found {
	if depth > 8 {
		return nil
	}
	var out []found
	switch t := v.(type) {
	case []any:
		for i, e := range t[:min(len(t), 3)] {
			out = append(out, priceKeys(e, fmt.Sprintf("%s[%d]", path, i), depth+1)...)
		}
	case map[string]any:
		cur := ""
		for _, k := range []string{"currency", "currencyCode", "priceCurrency", "currency_code"} {
			if s, ok := t[k].(string); ok && isoCodes[strings.ToUpper(s)] {
				cur = strings.ToUpper(s)
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			lk := strings.ToLower(k)
			if isNumberish(t[k]) && (strings.Contains(lk, "price") || lk == "amount" || lk == "value" && cur != "") {
				out = append(out, found{join(path, k), cur})
			}
		}
		for _, k := range keys {
			if _, leaf := t[k].(map[string]any); leaf || isList(t[k]) {
				out = append(out, priceKeys(t[k], join(path, k), depth+1)...)
			}
		}
	}
	return out
}

func isList(v any) bool { _, ok := v.([]any); return ok }

func isNumberish(v any) bool {
	switch t := v.(type) {
	case json.Number:
		return true
	case string:
		return machineRe.MatchString(strings.TrimSpace(t))
	}
	return false
}

func isType(m map[string]any, name string) bool {
	switch t := m["@type"].(type) {
	case string:
		return t == name
	case []any:
		for _, e := range t {
			if e == name {
				return true
			}
		}
	}
	return false
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
