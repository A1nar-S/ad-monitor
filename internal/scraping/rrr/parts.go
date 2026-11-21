package rrr

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// Attr keys of an rrr.lt listing. Ids are strings, as the API has them.
const (
	attrCategoryID = "category_id"
	attrCategory   = "category"
	attrMakeID     = "make_id"
	attrModelID    = "model_id"
	attrMake       = "make"
	attrModel      = "model"
	attrYear       = "year"
	attrFuel       = "fuel"    // the donor car's, FuelTypeFacetId, e.g. DIESEL
	attrGearbox    = "gearbox" // the donor car's, e.g. MANUAL
	attrQuality    = "quality" // ProductQuality, e.g. USED
	attrSide       = "side"    // PartPosition, e.g. LEFT_FRONT
	attrEngineCC   = "engine_cc"
	attrPowerKW    = "power_kw"
	// TextAttr holds the part numbers and the donor car's description, for
	// the keywords filter (the title is checked too).
	TextAttr = "text"
)

// partsQuery is one page of a feed: the fields a listing is built from.
const partsQuery = `query feedPage($first: Int, $after: String, $input: ProductsSearchInput!) {
  searchProducts(first: $first, after: $after, input: $input) {
    edges { node {
      id sku url createdAt quality sold
      price(currencyCode: "EUR") { amount }
      category { id name }
      images { ... on MainImage { url } }
      ... on Part {
        position
        partNumbers { number }
        vehicle {
          description
          model { id name series { manufacturer { id name } } }
          attributes { type value }
        }
      }
    } }
  }
}`

type partsPage struct {
	SearchProducts struct {
		Edges []struct {
			Node part `json:"node"`
		} `json:"edges"`
	} `json:"searchProducts"`
}

type part struct {
	ID        int    `json:"id"`
	SKU       string `json:"sku"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	Quality   string `json:"quality"`
	Sold      bool   `json:"sold"`
	Position  string `json:"position"`
	Price     *struct {
		Amount string `json:"amount"`
	} `json:"price"`
	Category struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"category"`
	Images []struct {
		URL string `json:"url"`
	} `json:"images"`
	PartNumbers []struct {
		Number string `json:"number"`
	} `json:"partNumbers"`
	Vehicle *struct {
		Description string `json:"description"`
		Model       *struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Series *struct {
				Manufacturer *struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
				} `json:"manufacturer"`
			} `json:"series"`
		} `json:"model"`
		Attributes []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"attributes"`
	} `json:"vehicle"`
}

// FetchFeed implements sources.Source: one page of a feed's query, newest
// parts first, 15 per page.
func (c *Client) FetchFeed(ctx context.Context, feed string, page int) ([]events.ListingDiscovered, error) {
	f, err := parseFeed(feed)
	if err != nil {
		return nil, err
	}
	vars := map[string]any{"first": pageSize, "input": f.input()}
	if page > 1 {
		vars["after"] = cursor(page)
	}
	var p partsPage
	if err := c.query(ctx, partsQuery, vars, &p); err != nil {
		return nil, err
	}
	return listings(p, feed, time.Now()), nil
}

func listings(p partsPage, feed string, now time.Time) []events.ListingDiscovered {
	out := make([]events.ListingDiscovered, 0, len(p.SearchProducts.Edges))
	for _, e := range p.SearchProducts.Edges {
		if e.Node.ID == 0 || e.Node.Sold {
			continue
		}
		out = append(out, e.Node.listing(feed, now))
	}
	return out
}

// imageBase serves the API's image paths ("/dwz/0/.../x.jpg") at a size that
// suits a notification thumbnail.
const imageBase = "https://images.ovoko.com/tl/fill/400x300"

func (n part) listing(feed string, now time.Time) events.ListingDiscovered {
	l := events.ListingDiscovered{
		Source:     Source,
		ExternalID: strconv.Itoa(n.ID),
		Feed:       feed,
		URL:        n.URL,
		PostedAt:   now,
		Attrs:      map[string]any{},
	}
	if t, err := time.Parse(time.RFC3339, n.CreatedAt); err == nil {
		l.PostedAt = t
	}
	if l.URL == "" && n.SKU != "" {
		l.URL = baseURL + "/en/used-part/" + n.SKU
	}
	for _, img := range n.Images {
		if img.URL != "" {
			l.ImageURL = imageBase + "/" + strings.TrimLeft(img.URL, "/")
			break
		}
	}
	if n.Price != nil {
		if v, err := strconv.ParseFloat(n.Price.Amount, 64); err == nil && v > 0 {
			l.PriceValue = &v
			l.Price = formatEUR(v)
		}
	}

	a := l.Attrs
	setStr := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			a[k] = v
		}
	}
	setStr(attrCategory, n.Category.Name)
	if n.Category.ID != 0 {
		a[attrCategoryID] = strconv.Itoa(n.Category.ID)
	}
	setStr(attrQuality, n.Quality)
	setStr(attrSide, n.Position)

	var car, desc string
	var numbers []string
	for _, pn := range n.PartNumbers {
		if s := strings.TrimSpace(pn.Number); s != "" {
			numbers = append(numbers, s)
		}
	}
	if v := n.Vehicle; v != nil {
		desc = cleanText(v.Description)
		if m := v.Model; m != nil {
			setStr(attrModel, m.Name)
			a[attrModelID] = strconv.Itoa(m.ID)
			if m.Series != nil && m.Series.Manufacturer != nil {
				setStr(attrMake, m.Series.Manufacturer.Name)
				a[attrMakeID] = strconv.Itoa(m.Series.Manufacturer.ID)
			} else if cb, err := PartsCodebook(); err == nil {
				// Without its make, a part would fail every make search that
				// meets it on another search's feed; the model says which.
				if mk, ok := cb.MakeOfModel(m.ID); ok {
					a[attrMake], a[attrMakeID] = mk.Name, strconv.Itoa(mk.ID)
				}
			}
		}
		for _, at := range v.Attributes {
			val := strings.TrimSpace(at.Value)
			if val == "" {
				continue
			}
			switch at.Type {
			case "YEARS":
				// A single year, but read "2010-2012" as its first: a part with
				// no year fails every year search that meets it elsewhere.
				setNum(a, attrYear, leadingNumber(val))
			case "ENGINE_CUBIC_CAPACITY":
				setNum(a, attrEngineCC, val)
			case "ENGINE_POWER":
				setNum(a, attrPowerKW, val)
			case "FUEL_TYPE":
				a[attrFuel] = enumID(val)
			case "GEARBOX_TYPE":
				a[attrGearbox] = enumID(val)
			}
		}
		car = strings.TrimSpace(strings.Join(nonEmpty(str(a, attrMake), str(a, attrModel)), " "))
		if y, ok := a[attrYear].(float64); ok {
			car += ", " + strconv.Itoa(int(y))
		}
		if car == "" {
			car = desc
		}
	}
	if text := strings.Join(nonEmpty(strings.Join(numbers, " "), desc), " "); text != "" {
		a[TextAttr] = text
	}

	l.Title = strings.Join(nonEmpty(n.Category.Name, car), " · ")
	if l.Title == "" {
		l.Title = n.SKU
	}
	l.Fields = fields(l, car, numbers)
	return l
}

func fields(l events.ListingDiscovered, car string, numbers []string) []events.Field {
	var fs []events.Field
	add := func(name, value string, inline bool) {
		if value != "" {
			fs = append(fs, events.Field{Name: name, Value: value, Inline: inline})
		}
	}
	add("🔧 Part", str(l.Attrs, attrCategory), false)
	add("🚗 Car", car, false)
	var engine []string
	if v, ok := l.Num(attrEngineCC); ok && v > 0 {
		engine = append(engine, fmt.Sprintf("%.1f l", v/1000))
	}
	if v, ok := l.Num(attrPowerKW); ok && v > 0 {
		engine = append(engine, fmt.Sprintf("%d kW", int(v)))
	}
	engine = append(engine, fuelNames[str(l.Attrs, attrFuel)])
	add("⚙️ Engine", strings.Join(nonEmpty(engine...), ", "), true)
	add("🕹️ Gearbox", gearboxNames[str(l.Attrs, attrGearbox)], true)
	add("↔️ Side", sideNames[str(l.Attrs, attrSide)], true)
	add("✨ Condition", labelOf(qualityOptions, str(l.Attrs, attrQuality)), true)
	if len(numbers) > 0 {
		add("🔢 Part number", strings.Join(numbers[:min(len(numbers), 3)], ", "), false)
	}
	return fs
}

var (
	brTag   = regexp.MustCompile(`(?i)<br\s*/?>`)
	anyTag  = regexp.MustCompile(`<[^>]*>`)
	notWord = regexp.MustCompile(`[^A-Z0-9]+`)
)

func cleanText(s string) string {
	s = anyTag.ReplaceAllString(brTag.ReplaceAllString(s, " "), "")
	return strings.Join(strings.Fields(s), " ")
}

// enumID turns a vehicle attribute as the API spells it ("diesel",
// "gasoline gas") into the facet id searches use ("DIESEL", "GASOLINE_GAS").
func enumID(v string) string {
	return strings.Trim(notWord.ReplaceAllString(strings.ToUpper(v), "_"), "_")
}

// leadingNumber is the digits a value starts with ("2010" of "2010-2012").
func leadingNumber(v string) string {
	v = strings.TrimSpace(v)
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	return v[:end]
}

func setNum(a map[string]any, key, v string) {
	if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n > 0 {
		a[key] = n
	}
}

func str(a map[string]any, key string) string {
	s, _ := a[key].(string)
	return s
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// formatEUR shows a price as the site does: "58.82 €", "120 €".
func formatEUR(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimSuffix(s, ".00")
	return s + " €"
}
