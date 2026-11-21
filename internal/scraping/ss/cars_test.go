package ss

import (
	"testing"
	"time"

	carrating "github.com/A1nar-S/advertisiment-monitoring/internal/deals"
)

// Every translated name must still be one of the codebook's (a codebook
// refresh may rename options), and every translation a term deals knows.
func TestCarTerms(t *testing.T) {
	cb, err := CarsCodebook()
	if err != nil {
		t.Fatal(err)
	}
	for key, terms := range carTerms {
		f, ok := cb.Field(key)
		if !ok {
			t.Errorf("no codebook field %q", key)
			continue
		}
		for name, term := range terms {
			if _, ok := f.Option(name); !ok {
				t.Errorf("%s %q is not a codebook option", key, name)
			}
			if !carrating.Knows(key, term) {
				t.Errorf("%s %q -> %q, which deals does not know", key, name, term)
			}
		}
	}

	c := NewClient(time.Second)
	if got := c.CarTerm("fuel", "Benzīns/gāze"); got != "petrol" {
		t.Errorf("petrol/gas = %q, want petrol", got)
	}
	if got := c.CarTerm("equipment", "Spoileris"); got != "" {
		t.Errorf("a spoiler = %q, want nothing: it says nothing about the spec", got)
	}
}
