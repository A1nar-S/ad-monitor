package pages

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// maxCents keeps amounts far from int64 overflow; no shop sells for more.
const maxCents = 1e13

// numberRe finds an amount as shops write it: "1 299", "1.299,00",
// "1,299.99", "1'299.-", "1299,-".
var numberRe = regexp.MustCompile(`\d[\d\s\x{00a0}\x{202f}\x{2009}.,'’]*`)

// machineRe is a number as a program writes it: dot decimals, no grouping.
var machineRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// isoCodes are the currency codes recognized when written out ("1299 EUR").
var isoCodes = map[string]bool{
	"EUR": true, "USD": true, "GBP": true, "SEK": true, "NOK": true, "DKK": true, "ISK": true, "PLN": true,
	"CHF": true, "CZK": true, "HUF": true, "RON": true, "BGN": true, "JPY": true, "CNY": true, "CAD": true,
	"AUD": true, "NZD": true, "INR": true, "UAH": true, "TRY": true, "RUB": true,
}

// symbols map currency signs to codes, longest first where one contains
// another ("US$" before "$").
var symbols = []struct{ sign, code string }{
	{"US$", "USD"}, {"€", "EUR"}, {"£", "GBP"}, {"$", "USD"}, {"¥", "JPY"}, {"₹", "INR"}, {"zł", "PLN"},
	{"Kč", "CZK"}, {"₴", "UAH"}, {"₺", "TRY"}, {"₽", "RUB"}, {"lei", "RON"}, {"Ft", "HUF"},
}

var codeRe = regexp.MustCompile(`\b[A-Z]{3}\b`)
var krRe = regexp.MustCompile(`(?i)(^|[\s\d])kr\.?($|[\s\d])`)

// DetectCurrency finds the currency a price text shows, or "". fallback
// settles "kr", which Sweden, Norway, Denmark and Iceland all write.
func DetectCurrency(text, fallback string) string {
	for _, m := range codeRe.FindAllString(text, -1) {
		if isoCodes[m] {
			return m
		}
	}
	for _, s := range symbols {
		if strings.Contains(text, s.sign) {
			return s.code
		}
	}
	if krRe.MatchString(text) {
		switch fallback {
		case "SEK", "NOK", "DKK", "ISK":
			return fallback
		}
	}
	return ""
}

func stripCurrency(s string) string {
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		if isoCodes[m] {
			return ""
		}
		return m
	})
	for _, sym := range symbols {
		s = strings.ReplaceAll(s, sym.sign, "")
	}
	return strings.Trim(s, ".,-–")
}

// GuessCurrency is the currency a web page shows most, for values that show
// none: shop meta tags first, else the commonest sign or code in its text.
func GuessCurrency(p *Page) string {
	if p.IsJSON() {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(p.Body))
	if err != nil {
		return ""
	}
	if c := metaCurrency(doc); c != "" {
		return c
	}
	doc.Find("script, style, noscript, template").Remove()
	text := doc.Find("body").Text()
	counts := map[string]int{}
	for _, m := range codeRe.FindAllString(text, -1) {
		if isoCodes[m] {
			counts[m]++
		}
	}
	for _, s := range symbols {
		if s.sign != "US$" && len(s.sign) <= 3 && !isLetters(s.sign) {
			counts[s.code] += strings.Count(text, s.sign)
		}
	}
	best := ""
	for code, n := range counts {
		if n > counts[best] || n == counts[best] && n > 0 && code < best {
			best = code
		}
	}
	return best
}

func isLetters(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// ParsePrice reads an amount and its currency from raw. machine is set for
// values a program wrote (JSON, attributes), where "1.299" means 1.299; in
// text a person reads, it means 1299. A text without a currency gets
// fallback.
func ParsePrice(raw, fallback string, machine bool) (sources.Price, error) {
	raw = strings.TrimSpace(raw)
	p := sources.Price{Currency: DetectCurrency(raw, fallback)}
	if p.Currency == "" {
		p.Currency = fallback
	}
	if machine && machineRe.MatchString(raw) {
		cents, err := decimalCents(raw)
		p.Cents = cents
		return p, err
	}
	m := numberRe.FindString(raw)
	if m == "" {
		return p, fmt.Errorf("%w: %q has no number", sources.ErrNoPrice, clip(raw, 60))
	}
	// A program's value is the amount and at most a currency ("€29.99",
	// "29,99 EUR"); "Whey Protein 1kg" is a name, not a price of 1.
	if machine && strings.TrimFunc(stripCurrency(strings.Replace(raw, m, "", 1)), unicode.IsSpace) != "" {
		return p, fmt.Errorf("%w: %q is not an amount", sources.ErrNoPrice, clip(raw, 60))
	}
	cents, err := humanCents(m)
	if err != nil {
		return p, fmt.Errorf("%w: %q: %w", sources.ErrNoPrice, clip(raw, 60), err)
	}
	p.Cents = cents
	return p, nil
}

// humanCents reads a number written for people. Grouping is spaces,
// apostrophes or whichever of . and , is not the decimal separator.
func humanCents(s string) (int64, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', ' ', ' ', ' ', '\'', '’', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
	// "1299,-" and "1.299,–" mean whole units; a trailing separator is
	// punctuation, not a decimal point.
	s = strings.TrimRight(s, ".,-–")
	dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case dot >= 0 && comma >= 0:
		dec, group := ".", ","
		if comma > dot {
			dec, group = ",", "."
		}
		s = strings.ReplaceAll(s, group, "")
		s = strings.Replace(s, dec, ".", 1)
	case dot >= 0 || comma >= 0:
		sep := "."
		if comma >= 0 {
			sep = ","
		}
		at := strings.LastIndex(s, sep)
		// One separator followed by exactly three digits is grouping
		// ("1.299", "1,299"); prices with three decimals don't exist.
		if strings.Count(s, sep) > 1 || len(s)-at-1 == 3 {
			s = strings.ReplaceAll(s, sep, "")
		} else {
			s = strings.Replace(s, sep, ".", 1)
		}
	}
	return decimalCents(s)
}

// decimalCents turns "1299", "1073.55" or "12.5" into hundredths, exactly;
// more decimals round half up.
func decimalCents(s string) (int64, error) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > maxCents/100 {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	cents := w * 100
	if frac != "" {
		for len(frac) < 3 {
			frac += "0"
		}
		f, err := strconv.ParseInt(frac[:3], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not an amount", s)
		}
		cents += (f + 5) / 10
	}
	if neg {
		return 0, fmt.Errorf("negative amount %q", s)
	}
	return cents, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
