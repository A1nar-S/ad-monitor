package rrr

import "testing"

// Every fact an rrr.lt alert shows has its Latvian name.
func TestFieldsNamedInLatvian(t *testing.T) {
	seen := 0
	for _, l := range fetchFixture(t) {
		for _, f := range l.Fields {
			seen++
			if f.NameIn("lv") == f.Name {
				t.Errorf("%q has no Latvian name", f.Name)
			}
		}
	}
	if seen == 0 {
		t.Fatal("the fixture listings show no facts")
	}
}
