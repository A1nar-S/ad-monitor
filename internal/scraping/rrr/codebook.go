package rrr

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
)

// Codebook is a snapshot of rrr.lt's taxonomy: the part category tree and
// every make's models. Searches are written and checked in its names; feeds
// and attrs use its ids. Refresh it with `go run ./cmd/admin rrr-codebook`.
type Codebook struct {
	Fetched    string     `json:"fetched"`
	Categories []Category `json:"categories"` // parents before their children
	Makes      []Make     `json:"makes"`

	// Built on load.
	byID      map[int]*Category
	byPath    map[string][]*Category // a path can name two categories
	byLeaf    map[string][]string    // leaf name -> the paths that end in it
	makeByKey map[string]*Make
	makeOf    map[int]*Make // model id -> its make
	paths     []string      // unique, in tree order
	leaves    []string      // unique leaf names
}

// Category is one node of the part tree, e.g. Engine › Turbocharger › Turbo.
type Category struct {
	ID     int    `json:"id"`
	Parent int    `json:"parent,omitempty"`
	Name   string `json:"name"`

	Path string `json:"-"` // "Engine › Turbocharger › Turbo"
}

// Make is a car manufacturer.
type Make struct {
	ID     int     `json:"id"`
	Name   string  `json:"name"`
	Models []Model `json:"models,omitempty"`

	labels    []string
	byLabel   map[string]*Model
	byName    map[string][]*Model
	modelByID map[int]*Model
}

// Model is one generation of a make, e.g. 3 E46 (1998–2005).
type Model struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	Label string `json:"-"`
}

// pathSep joins category names into the option users pick: names repeat in
// different branches ("Front bumper" is a group and a part), paths do not.
const pathSep = " › "

//go:embed codebook/parts.json
var partsJSON []byte

var (
	codebookOnce sync.Once
	codebook     *Codebook
	codebookErr  error
)

// PartsCodebook returns the embedded snapshot.
func PartsCodebook() (*Codebook, error) {
	codebookOnce.Do(func() { codebook, codebookErr = ParseCodebook(partsJSON) })
	return codebook, codebookErr
}

// ParseCodebook reads a snapshot and builds its indexes.
func ParseCodebook(raw []byte) (*Codebook, error) {
	var cb Codebook
	if err := json.Unmarshal(raw, &cb); err != nil {
		return nil, fmt.Errorf("rrr codebook: %w", err)
	}
	cb.index()
	return &cb, nil
}

func (cb *Codebook) index() {
	cb.byID = make(map[int]*Category, len(cb.Categories))
	cb.byPath = make(map[string][]*Category, len(cb.Categories))
	cb.byLeaf = map[string][]string{}
	cb.paths, cb.leaves = nil, nil
	for i := range cb.Categories {
		c := &cb.Categories[i]
		c.Path = c.Name
		if p, ok := cb.byID[c.Parent]; ok {
			c.Path = p.Path + pathSep + c.Name
		}
		cb.byID[c.ID] = c
		key := pathKey(c.Path)
		if len(cb.byPath[key]) == 0 {
			cb.paths = append(cb.paths, c.Path)
			if len(cb.byLeaf[textutil.Normalize(c.Name)]) == 0 {
				cb.leaves = append(cb.leaves, c.Name)
			}
			cb.byLeaf[textutil.Normalize(c.Name)] = append(cb.byLeaf[textutil.Normalize(c.Name)], c.Path)
		}
		cb.byPath[key] = append(cb.byPath[key], c)
	}
	cb.makeByKey = make(map[string]*Make, len(cb.Makes))
	cb.makeOf = map[int]*Make{}
	for i := range cb.Makes {
		m := &cb.Makes[i]
		cb.makeByKey[textutil.Normalize(m.Name)] = m
		m.index()
		for _, o := range m.Models {
			cb.makeOf[o.ID] = m
		}
	}
}

func (m *Make) index() {
	m.labels = make([]string, 0, len(m.Models))
	m.byLabel = make(map[string]*Model, len(m.Models))
	m.byName = map[string][]*Model{}
	m.modelByID = make(map[int]*Model, len(m.Models))
	seen := map[string]bool{}
	for i := range m.Models {
		o := &m.Models[i]
		o.Label = o.Name
		if o.From != "" || o.To != "" {
			o.Label += " (" + o.From + "–" + o.To + ")"
		}
		if seen[textutil.Normalize(o.Label)] {
			o.Label += " #" + strconv.Itoa(o.ID)
		}
		seen[textutil.Normalize(o.Label)] = true
		m.labels = append(m.labels, o.Label)
		m.byLabel[textutil.Normalize(o.Label)] = o
		m.byName[textutil.Normalize(o.Name)] = append(m.byName[textutil.Normalize(o.Name)], o)
		m.modelByID[o.ID] = o
	}
}

// CategoryByID finds a category; ok is false for one newer than the snapshot.
func (cb *Codebook) CategoryByID(id int) (*Category, bool) {
	c, ok := cb.byID[id]
	return c, ok
}

// Ancestors lists id and every category above it, nearest first. An id the
// snapshot lacks is returned alone.
func (cb *Codebook) Ancestors(id int) []int {
	out := []int{id}
	for c, ok := cb.byID[id]; ok && c.Parent != 0; c, ok = cb.byID[c.Parent] {
		if slices.Contains(out, c.Parent) {
			break // a cycle would be a broken snapshot; do not loop
		}
		out = append(out, c.Parent)
	}
	return out
}

// PartOptions lists every category path for a form: the top groups first,
// then their subgroups, then single parts, each level in tree order. A list
// that shows its first few options then offers the groups ("Wheels/tires/caps"),
// not 50 air-conditioning parts.
func (cb *Codebook) PartOptions() []string {
	out := slices.Clone(cb.paths)
	slices.SortStableFunc(out, func(a, b string) int {
		return strings.Count(a, pathSep) - strings.Count(b, pathSep)
	})
	return out
}

// MakeOfModel finds the make a model id belongs to.
func (cb *Codebook) MakeOfModel(model int) (*Make, bool) {
	m, ok := cb.makeOf[model]
	return m, ok
}

// MakeByName finds a make, ignoring case and diacritics.
func (cb *Codebook) MakeByName(name string) (*Make, bool) {
	m, ok := cb.makeByKey[textutil.Normalize(name)]
	return m, ok
}

func (cb *Codebook) makeNames() []string {
	out := make([]string, len(cb.Makes))
	for i, m := range cb.Makes {
		out[i] = m.Name
	}
	return out
}

// Model finds a model by its label ("3 E46 (1998–2005)") or, when only one
// generation has it, by its bare name ("3 E46").
func (m *Make) Model(name string) (*Model, bool) {
	if o, ok := m.byLabel[textutil.Normalize(name)]; ok {
		return o, true
	}
	if os := m.byName[textutil.Normalize(name)]; len(os) == 1 {
		return os[0], true
	}
	return nil, false
}

// --- Building the snapshot from the live site -------------------------------

// taxonomyQuery reads the whole taxonomy in one request: four levels of part
// categories (the deepest the site has) and every make's models.
const taxonomyQuery = `{
  manufacturers(first: 1000, input: {orderBy: {name: ASC}}) {
    edges { node { id name
      modelCategories(first: 1000) { edges { node { id
        models(first: 1000) { edges { node { id name yearStart yearEnd } } }
      } } }
    } }
  }
  categories(first: 1000) {
    edges { node { id name
      categories(first: 1000) { edges { node { id name
        categories(first: 1000) { edges { node { id name
          categories(first: 1000) { edges { node { id name
            categories(first: 1000) { edges { node { id name } } }
          } } }
        } } }
      } } }
    } }
  }
}`

type taxCategory struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Categories *struct {
		Edges []taxEdge `json:"edges"`
	} `json:"categories"`
}

type taxEdge struct {
	Node taxCategory `json:"node"`
}

type taxonomy struct {
	Manufacturers struct {
		Edges []struct {
			Node struct {
				ID              int    `json:"id"`
				Name            string `json:"name"`
				ModelCategories struct {
					Edges []struct {
						Node struct {
							Models struct {
								Edges []struct {
									Node struct {
										ID        int     `json:"id"`
										Name      string  `json:"name"`
										YearStart *string `json:"yearStart"`
										YearEnd   *string `json:"yearEnd"`
									} `json:"node"`
								} `json:"edges"`
							} `json:"models"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"modelCategories"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"manufacturers"`
	Categories struct {
		Edges []taxEdge `json:"edges"`
	} `json:"categories"`
}

// BuildCodebook reads the taxonomy from the API: one request.
func (c *Client) BuildCodebook(ctx context.Context) (*Codebook, error) {
	if c.gap > 0 {
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}
	}
	var t taxonomy
	if err := c.query(ctx, taxonomyQuery, nil, &t); err != nil {
		return nil, err
	}
	cb := codebookFrom(t, time.Now())
	if len(cb.Categories) == 0 || len(cb.Makes) == 0 {
		return nil, fmt.Errorf("rrr codebook: empty taxonomy (%d categories, %d makes)", len(cb.Categories), len(cb.Makes))
	}
	return cb, nil
}

func codebookFrom(t taxonomy, now time.Time) *Codebook {
	cb := &Codebook{Fetched: now.UTC().Format("2006-01-02")}
	var walk func(parent int, n taxCategory)
	walk = func(parent int, n taxCategory) {
		cb.Categories = append(cb.Categories, Category{ID: n.ID, Parent: parent, Name: strings.TrimSpace(n.Name)})
		if n.Categories == nil {
			return
		}
		kids := n.Categories.Edges
		slices.SortStableFunc(kids, func(a, b taxEdge) int {
			return strings.Compare(textutil.Normalize(a.Node.Name), textutil.Normalize(b.Node.Name))
		})
		for _, k := range kids {
			walk(n.ID, k.Node)
		}
	}
	for _, e := range t.Categories.Edges {
		walk(0, e.Node)
	}

	for _, e := range t.Manufacturers.Edges {
		m := Make{ID: e.Node.ID, Name: strings.TrimSpace(e.Node.Name)}
		for _, s := range e.Node.ModelCategories.Edges {
			for _, me := range s.Node.Models.Edges {
				n := me.Node
				m.Models = append(m.Models, Model{ID: n.ID, Name: strings.TrimSpace(n.Name), From: deref(n.YearStart), To: deref(n.YearEnd)})
			}
		}
		slices.SortStableFunc(m.Models, func(a, b Model) int {
			if d := strings.Compare(textutil.Normalize(a.Name), textutil.Normalize(b.Name)); d != 0 {
				return d
			}
			return strings.Compare(a.From, b.From)
		})
		cb.Makes = append(cb.Makes, m)
	}
	cb.index()
	return cb
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// MarshalIndent is the snapshot as it is checked in: one category or make
// per line, so a refresh diffs readably.
func (cb *Codebook) MarshalIndent() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{\n  \"fetched\": ")
	f, _ := json.Marshal(cb.Fetched)
	b.Write(f)
	b.WriteString(",\n  \"categories\": [\n")
	for i, c := range cb.Categories {
		line, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		b.WriteString("    ")
		b.Write(line)
		if i < len(cb.Categories)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ],\n  \"makes\": [\n")
	for i, m := range cb.Makes {
		line, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		b.WriteString("    ")
		b.Write(line)
		if i < len(cb.Makes)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ]\n}\n")
	return []byte(b.String()), nil
}

// --- name matching -----------------------------------------------------------

// pathKey is how a category path is looked up: folded, with "›" or ">" as
// separator and the spaces around it ignored ("engine>turbo").
func pathKey(p string) string {
	parts := strings.FieldsFunc(textutil.Normalize(p), func(r rune) bool { return r == '›' || r == '>' })
	for i, s := range parts {
		parts[i] = strings.TrimSpace(s)
	}
	return strings.Join(parts, "›")
}
