package rrr

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// A feed is one API query, written as a canonical string of every constraint
// the site filters on, keys in a fixed order:
//
//	cat=230,1387&make=5&model=1:23&year=2005-2012&price=-300&quality=USED&side=LEFT,LEFT_FRONT
//
// cat: part category ids (a category includes everything below it); make:
// makes searched whole; model: make:model pairs; year/price: from-to, either
// side open; the rest: the API's own enum ids. Equal searches give equal
// strings, so they share one query.
type feedSpec struct {
	Cats    []int
	Makes   []int
	Models  [][2]int // {make, model}
	Year    span
	Price   span
	Quality []string
	Side    []string // PositionFacetId
}

// span is an inclusive integer bound; nil is open.
type span struct{ Min, Max *int }

func (s span) empty() bool { return s.Min == nil && s.Max == nil }

func (s span) String() string {
	var b strings.Builder
	if s.Min != nil {
		b.WriteString(strconv.Itoa(*s.Min))
	}
	b.WriteByte('-')
	if s.Max != nil {
		b.WriteString(strconv.Itoa(*s.Max))
	}
	return b.String()
}

func (s span) contains(v float64) bool {
	return (s.Min == nil || v >= float64(*s.Min)) && (s.Max == nil || v <= float64(*s.Max))
}

func (s span) input() map[string]any {
	out := map[string]any{}
	if s.Min != nil {
		out["from"] = *s.Min
	}
	if s.Max != nil {
		out["to"] = *s.Max
	}
	return out
}

func parseSpan(v string) (span, error) {
	lo, hi, ok := strings.Cut(v, "-")
	if !ok {
		return span{}, fmt.Errorf("range %q has no '-'", v)
	}
	var s span
	for _, p := range []struct {
		in  string
		out **int
	}{{lo, &s.Min}, {hi, &s.Max}} {
		if p.in == "" {
			continue
		}
		n, err := strconv.Atoi(p.in)
		if err != nil {
			return span{}, fmt.Errorf("range %q: %w", v, err)
		}
		*p.out = &n
	}
	return s, nil
}

// String encodes the feed canonically: lists sorted, empty parts left out.
func (f feedSpec) String() string {
	var parts []string
	ints := func(key string, vs []int) {
		if len(vs) == 0 {
			return
		}
		vs = slices.Clone(vs)
		slices.Sort(vs)
		vs = slices.Compact(vs)
		ss := make([]string, len(vs))
		for i, v := range vs {
			ss[i] = strconv.Itoa(v)
		}
		parts = append(parts, key+"="+strings.Join(ss, ","))
	}
	strs := func(key string, vs []string) {
		if len(vs) == 0 {
			return
		}
		vs = slices.Clone(vs)
		slices.Sort(vs)
		parts = append(parts, key+"="+strings.Join(slices.Compact(vs), ","))
	}
	ints("cat", f.Cats)
	ints("make", f.Makes)
	if len(f.Models) > 0 {
		ms := slices.Clone(f.Models)
		slices.SortFunc(ms, func(a, b [2]int) int {
			if a[0] != b[0] {
				return a[0] - b[0]
			}
			return a[1] - b[1]
		})
		ms = slices.Compact(ms)
		ss := make([]string, len(ms))
		for i, m := range ms {
			ss[i] = strconv.Itoa(m[0]) + ":" + strconv.Itoa(m[1])
		}
		parts = append(parts, "model="+strings.Join(ss, ","))
	}
	if !f.Year.empty() {
		parts = append(parts, "year="+f.Year.String())
	}
	if !f.Price.empty() {
		parts = append(parts, "price="+f.Price.String())
	}
	strs("quality", f.Quality)
	strs("side", f.Side)
	return strings.Join(parts, "&")
}

func parseFeed(feed string) (feedSpec, error) {
	var f feedSpec
	if feed == "" {
		return f, nil
	}
	for _, part := range strings.Split(feed, "&") {
		key, val, ok := strings.Cut(part, "=")
		if !ok || val == "" {
			return f, fmt.Errorf("feed %q: bad part %q", feed, part)
		}
		list := strings.Split(val, ",")
		var err error
		switch key {
		case "cat":
			f.Cats, err = atois(list)
		case "make":
			f.Makes, err = atois(list)
		case "model":
			for _, p := range list {
				mk, md, ok := strings.Cut(p, ":")
				if !ok {
					return f, fmt.Errorf("feed %q: model %q is not make:model", feed, p)
				}
				ids, e := atois([]string{mk, md})
				if e != nil {
					return f, fmt.Errorf("feed %q: %w", feed, e)
				}
				f.Models = append(f.Models, [2]int{ids[0], ids[1]})
			}
		case "year":
			f.Year, err = parseSpan(val)
		case "price":
			f.Price, err = parseSpan(val)
		case "quality":
			f.Quality = list
		case "side":
			f.Side = list
		default:
			return f, fmt.Errorf("feed %q: unknown key %q", feed, key)
		}
		if err != nil {
			return f, fmt.Errorf("feed %q: %w", feed, err)
		}
	}
	return f, nil
}

func atois(ss []string) ([]int, error) {
	out := make([]int, len(ss))
	for i, s := range ss {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

// input is the feed as the API's ProductsSearchInput, newest first.
func (f feedSpec) input() map[string]any {
	in := map[string]any{"sort": []map[string]string{{"property": "CREATED_AT", "direction": "DESC"}}}
	if len(f.Cats) > 0 {
		ids := make([]string, len(f.Cats))
		for i, c := range f.Cats {
			ids[i] = strconv.Itoa(c)
		}
		in["categoryIds"] = ids
	}
	var vehicles []map[string]string
	for _, m := range f.Makes {
		vehicles = append(vehicles, map[string]string{"manufacturerId": strconv.Itoa(m)})
	}
	for _, m := range f.Models {
		vehicles = append(vehicles, map[string]string{"manufacturerId": strconv.Itoa(m[0]), "modelId": strconv.Itoa(m[1])})
	}
	if len(vehicles) > 0 {
		in["vehicleModels"] = vehicles
	}
	if !f.Year.empty() {
		in["modelYear"] = f.Year.input()
	}
	if !f.Price.empty() {
		in["price"] = f.Price.input()
	}
	for key, vs := range map[string][]string{"qualityIds": f.Quality, "partPositionIds": f.Side} {
		if len(vs) > 0 {
			in[key] = vs
		}
	}
	return in
}

// PlanFeeds implements sources.Source. Feeds are never merged: rrr.lt gets
// thousands of parts a day, so a broader query would cost more pages than
// the narrow ones it replaced. Equal searches already share a feed string.
func (*Client) PlanFeeds(wanted []string, _ int) []string {
	out := slices.Clone(wanted)
	slices.Sort(out)
	return slices.Compact(out)
}

// InScope implements sources.Source. A part is "new" once, from whichever
// feed saw it first, so a search must recognise parts that came from another
// search's feed: they are checked against the feed's constraints by their
// attrs. A part from the search's own feed was filtered by the site already.
func (*Client) InScope(wanted []string, l events.ListingDiscovered) bool {
	cb, _ := PartsCodebook()
	for _, w := range wanted {
		if w == l.Feed {
			return true
		}
		f, err := parseFeed(w)
		if err == nil && f.matches(cb, l) {
			return true
		}
	}
	return false
}

func (f feedSpec) matches(cb *Codebook, l events.ListingDiscovered) bool {
	if len(f.Cats) > 0 {
		id, ok := intAttr(l, attrCategoryID)
		if !ok {
			return false
		}
		chain := []int{id}
		if cb != nil {
			chain = cb.Ancestors(id)
		}
		if !slices.ContainsFunc(chain, func(c int) bool { return slices.Contains(f.Cats, c) }) {
			return false
		}
	}
	if len(f.Makes) > 0 || len(f.Models) > 0 {
		mk, okMake := intAttr(l, attrMakeID)
		md, okModel := intAttr(l, attrModelID)
		if !okMake {
			return false
		}
		if !slices.Contains(f.Makes, mk) && (!okModel || !slices.Contains(f.Models, [2]int{mk, md})) {
			return false
		}
	}
	if !f.Year.empty() {
		v, ok := l.Num(attrYear)
		if !ok || !f.Year.contains(v) {
			return false
		}
	}
	if !f.Price.empty() && (l.PriceValue == nil || !f.Price.contains(*l.PriceValue)) {
		return false
	}
	for key, allowed := range map[string][]string{attrQuality: f.Quality, attrSide: f.Side} {
		if len(allowed) == 0 {
			continue
		}
		v, ok := l.Str(key)
		if !ok || !slices.Contains(allowed, v) {
			return false
		}
	}
	return true
}

// intAttr reads an id attr; they are stored as strings ("230").
func intAttr(l events.ListingDiscovered, key string) (int, bool) {
	s, ok := l.Str(key)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// pageSize is what the API returns at most per page, whatever is asked.
const pageSize = 15

// cursor is the API's "after" for a 1-based page: a Relay array-connection
// offset of the last item before it.
func cursor(page int) string {
	return base64.StdEncoding.EncodeToString([]byte("arrayconnection:" + strconv.Itoa(pageSize*(page-1)-1)))
}
