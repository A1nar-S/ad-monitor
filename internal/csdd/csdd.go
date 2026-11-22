// Package csdd counts CSDD's open technical inspection data ("Tehniskās
// apskates", data.gov.lv, CC0) into the table deals rates a model's
// reliability by: how many cars of each model, fuel and model year came to
// their main (periodic) inspection, and how many failed it at that first
// attempt.
//
// Each file is a list of inspections, Windows-1257, comma-separated with
// padded columns: date, make and model, model year, fuel, inspection type,
// result. The data names no vehicle, so it is about models, not cars.
package csdd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
)

// PackageURL is the dataset on data.gov.lv's CKAN API.
const PackageURL = "https://data.gov.lv/dati/api/3/action/package_show?id=tehniskas-apskates"

// Source names the data, for the table.
const Source = "CSDD Tehniskās apskates, data.gov.lv, CC0 1.0"

// File is one downloadable list: the year it covers and where it is.
type File struct {
	Year int
	URL  string
}

// fileYear reads "ta2024_1_6.lst" or "ta2021.csv" as 2024 or 2021.
var fileYear = regexp.MustCompile(`/ta(\d{4})[^/]*$`)

// userAgent names the client generically.
const userAgent = "advertisement-monitor/1.0 (open data refresh)"

// NewHTTPClient is a client for the downloads: no overall timeout, as a year
// is ~230 MB, but a server that stops answering is given up on.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       time.Minute,
	}}
}

// Get starts a GET of url; the caller closes the body.
func Get(ctx context.Context, c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

// Files lists the dataset's files (at pkgURL, normally PackageURL) for its
// newest years inspection years, oldest first. A year may come in parts
// ("ta2024_1_6", "ta2024_7_12"), each a different half.
func Files(ctx context.Context, c *http.Client, pkgURL string, years int) ([]File, error) {
	resp, err := Get(ctx, c, pkgURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var pkg struct {
		Result struct {
			Resources []struct {
				URL string `json:"url"`
			} `json:"resources"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return nil, fmt.Errorf("%s: %w", pkgURL, err)
	}
	var files []File
	for _, r := range pkg.Result.Resources {
		if m := fileYear.FindStringSubmatch(r.URL); m != nil {
			y, _ := strconv.Atoi(m[1])
			files = append(files, File{Year: y, URL: r.URL})
		}
	}
	slices.SortStableFunc(files, func(a, b File) int { return a.Year - b.Year })
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: no inspection files", pkgURL)
	}
	newest := files[len(files)-1].Year
	return slices.DeleteFunc(files, func(f File) bool { return f.Year <= newest-years }), nil
}

// Make is a make as the ads name it, with its models.
type Make struct {
	Name   string
	Models []string
}

// Counter counts inspections into a table.
type Counter struct {
	makes  []makeKeys
	stats  deals.InspectionStats
	models map[[2]string]*deals.ModelStats // by make and model name
	// Read, Counted: lines read and main inspections of cars counted.
	Read, Counted int
}

type makeKeys struct {
	name, folded string
	models       []modelKey
}

type modelKey struct{ name, key string }

// aliases are CSDD's make names that the ads spell otherwise.
var aliases = [][2]string{
	{"vw", "volkswagen"},
	{"mercedes benz", "mercedes"},
	{"mercedes-benz", "mercedes"},
	{"vaz", "lada"},
}

// NewCounter counts the cars of makes; any other inspection (a truck, a
// trailer, a tractor) is skipped.
func NewCounter(makes []Make) *Counter {
	c := &Counter{models: map[[2]string]*deals.ModelStats{}}
	c.stats.Source, c.stats.Base = Source, map[deals.Fuel]deals.Series{}
	for _, m := range makes {
		mk := makeKeys{name: m.Name, folded: textutil.Normalize(m.Name)}
		for _, model := range m.Models {
			if k := deals.ModelKey(m.Name, model); k != "" && !strings.EqualFold(model, "citi") {
				mk.models = append(mk.models, modelKey{model, k})
			}
		}
		c.makes = append(c.makes, mk)
	}
	// Longer names first, so "Land Rover" wins over a "Land".
	slices.SortStableFunc(c.makes, func(a, b makeKeys) int { return len(b.folded) - len(a.folded) })
	return c
}

// Count reads one file of year's inspections.
func (c *Counter) Count(r io.Reader, year int) error {
	if c.stats.Years[0] == 0 || year < c.stats.Years[0] {
		c.stats.Years[0] = year
	}
	c.stats.Years[1] = max(c.stats.Years[1], year)
	sc := bufio.NewScanner(charmap.Windows1257.NewDecoder().Reader(r))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		c.Read++
		c.line(sc.Text(), year)
	}
	return sc.Err()
}

// line counts one inspection if it is a car's main one (not a re-check). A make and model
// may hold commas ("AUDI 80 1,8 S"), so the fixed columns are read from
// both ends.
func (c *Counter) line(s string, year int) {
	cols := strings.Split(s, ",")
	if len(cols) < 6 {
		return
	}
	n := len(cols)
	kind, result := strings.TrimSpace(cols[n-2]), strings.TrimSpace(cols[n-1])
	if kind != "Pamatpārbaude" { // not a repeat or a roadside check's follow-up
		return
	}
	res, err := strconv.Atoi(result)
	if err != nil {
		return
	}
	model, err := strconv.Atoi(strings.TrimSpace(cols[n-4]))
	if err != nil || model < 1950 || model > year {
		return
	}
	fuel, ok := Fuel(cols[n-3])
	if !ok {
		return
	}
	brand, name, ok := c.Resolve(strings.Join(cols[1:n-4], ","))
	if !ok {
		return
	}
	// 0 no defects, 1 minor ones; 2 and up a re-inspection is ordered.
	failed := res >= 2
	m := c.models[[2]string{brand, name}]
	if m == nil {
		m = &deals.ModelStats{Make: brand, Model: name, Fuels: map[deals.Fuel]deals.Series{}}
		c.models[[2]string{brand, name}] = m
	}
	add(m.Fuels, fuel, model, failed)
	add(c.stats.Base, fuel, model, failed)
	c.Counted++
}

func add(by map[deals.Fuel]deals.Series, f deals.Fuel, year int, failed bool) {
	s := by[f]
	s.Add(year, failed)
	by[f] = s
}

// Fuel translates CSDD's fuel into deals' words: LPG and CNG count as
// petrol, as on the ads' list pages; electric with another fuel is a hybrid.
func Fuel(s string) (deals.Fuel, bool) {
	s = textutil.Normalize(s)
	electric := strings.Contains(s, "elektr")
	switch {
	case s == "":
		return "", false
	case electric && strings.Contains(s, " un "):
		return deals.Hybrid, true
	case electric:
		return deals.Electric, true
	case strings.Contains(s, "dizel"):
		return deals.Diesel, true
	case strings.Contains(s, "benzin"), strings.Contains(s, "gaze"):
		return deals.Petrol, true
	}
	return "", false
}

// Resolve finds the make and model that CSDD's "MERCEDES BENZ GLE 350 D
// 4MATIC" means in the ads' names: the make, then the longest of its models
// that covers it ("GLE 350"). A name more general than any model ("VW
// GOLF", which the ads split into Golf 1 … Golf 8) keeps CSDD's word, which
// then covers those models. ok is false for anything not a car of makes.
func (c *Counter) Resolve(raw string) (brand, model string, ok bool) {
	name := textutil.Normalize(strings.ReplaceAll(raw, ",", "."))
	for _, a := range aliases {
		if rest, ok := strings.CutPrefix(name, a[0]+" "); ok {
			name = a[1] + " " + rest
			break
		}
	}
	var mk *makeKeys
	var rest string
	for i := range c.makes {
		if r, ok := strings.CutPrefix(name, c.makes[i].folded+" "); ok {
			mk, rest = &c.makes[i], r
			break
		}
	}
	if mk == nil {
		return "", "", false
	}
	key := deals.ModelKey(mk.name, rest)
	if key == "" {
		return "", "", false
	}
	best := modelKey{}
	general := false
	for _, m := range mk.models {
		switch {
		case deals.CoversModel(m.key, key) && len(m.key) > len(best.key):
			best = m
		case deals.CoversModel(key, m.key):
			general = true
		}
	}
	switch {
	case best.key != "":
		return mk.name, best.name, true
	case general:
		return mk.name, titleCase(rest), true
	}
	return "", "", false
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		words[i] = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return strings.Join(words, " ")
}

// Table is what was counted, without models too rare to ever be rated.
func (c *Counter) Table(minInspections int) deals.InspectionStats {
	st := c.stats
	st.Models = nil
	for _, m := range c.models {
		for f, s := range m.Fuels {
			if s.Total() < minInspections {
				delete(m.Fuels, f)
			}
		}
		if len(m.Fuels) > 0 {
			st.Models = append(st.Models, *m)
		}
	}
	slices.SortFunc(st.Models, func(a, b deals.ModelStats) int {
		return strings.Compare(a.Make+"\x00"+a.Model, b.Make+"\x00"+b.Model)
	})
	return st
}

// Marshal writes the table one model a line, so a yearly refresh is a
// readable diff.
func Marshal(st deals.InspectionStats) ([]byte, error) {
	head, err := json.Marshal(struct {
		Source string                      `json:"source"`
		Years  [2]int                      `json:"years"`
		Base   map[deals.Fuel]deals.Series `json:"base"`
	}{st.Source, st.Years, st.Base})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.Write(head[:len(head)-1])
	b.WriteString(`,"models":[`)
	for i, m := range st.Models {
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("\n")
		b.Write(raw)
	}
	b.WriteString("\n]}\n")
	return []byte(b.String()), nil
}
