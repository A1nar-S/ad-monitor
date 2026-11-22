package csdd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
)

var testMakes = []Make{
	{Name: "Volkswagen", Models: []string{"Golf 4", "Golf 5", "Golf Plus", "Passat (B6)", "Passat Variant", "Citi"}},
	{Name: "Mercedes", Models: []string{"C220", "GLE 350", "Sprinter"}},
	{Name: "Toyota", Models: []string{"RAV 4", "Corolla", "Corolla Verso"}},
	{Name: "Mazda", Models: []string{"Mazda6", "626"}},
	{Name: "Land Rover", Models: []string{"Range Rover", "Range Rover Sport"}},
	{Name: "Audi", Models: []string{"80", "A4"}},
}

func TestResolve(t *testing.T) {
	c := NewCounter(testMakes)
	tests := []struct{ raw, brand, model string }{
		{"VW GOLF PLUS", "Volkswagen", "Golf Plus"},
		{"VW GOLF", "Volkswagen", "Golf"}, // more general than any model: kept, covers Golf 4 and 5
		{"VW PASSAT VARIANT", "Volkswagen", "Passat Variant"},
		{"MERCEDES BENZ GLE 350 D 4MATIC", "Mercedes", "GLE 350"},
		{"MERCEDES BENZ C220", "Mercedes", "C220"},
		{"MERCEDES-BENZ SPRINTER 313", "Mercedes", "Sprinter"},
		{"TOYOTA RAV4", "Toyota", "RAV 4"},
		{"TOYOTA COROLLA VERSO", "Toyota", "Corolla Verso"},
		{"MAZDA 6", "Mazda", "Mazda6"},
		{"MAZDA 626", "Mazda", "626"},
		{"LAND ROVER RANGE ROVER SPORT", "Land Rover", "Range Rover Sport"},
		{"AUDI 80 1,8 S", "Audi", "80"},
	}
	for _, tt := range tests {
		b, m, ok := c.Resolve(tt.raw)
		if !ok || b != tt.brand || m != tt.model {
			t.Errorf("Resolve(%q) = %q %q %v, want %q %q", tt.raw, b, m, ok, tt.brand, tt.model)
		}
	}
	for _, raw := range []string{"SCANIA R 450", "MERCEDES BENZ ACTROS", "VW", "MAZDA 3"} {
		if b, m, ok := c.Resolve(raw); ok {
			t.Errorf("Resolve(%q) = %q %q, want none", raw, b, m)
		}
	}
}

func TestFuel(t *testing.T) {
	for raw, want := range map[string]deals.Fuel{
		"Dīzeļdegviela   ": deals.Diesel, "Benzīns": deals.Petrol, "Benzīns un naftas gāze": deals.Petrol,
		"Dabas gāze": deals.Petrol, "Elektrība": deals.Electric, "Elektrība un benzīns": deals.Hybrid,
		"Elektrība un dīzeļdegviela": deals.Hybrid, "": "",
	} {
		if got, _ := Fuel(raw); got != want {
			t.Errorf("Fuel(%q) = %q, want %q", raw, got, want)
		}
	}
}

// lines are padded like CSDD's files.
const lines = `02.01.2024 ,VW GOLF                                    , 2010,Dīzeļdegviela          ,Pamatpārbaude     ,          2
02.01.2024 ,VW GOLF                                    , 2010,Dīzeļdegviela          ,Pamatpārbaude     ,          1
02.01.2024 ,VW GOLF                                    , 2010,Dīzeļdegviela          ,Atkārtota         ,          0
02.01.2024 ,VW GOLF PLUS                               , 2011,Benzīns                ,Pamatpārbaude     ,          3
02.01.2024 ,AUDI 80 1,8 S                              , 1990,Benzīns                ,Pamatpārbaude     ,          0
02.01.2024 ,SCANIA R 450                               , 2015,Dīzeļdegviela          ,Pamatpārbaude     ,          2
02.01.2024 ,RB 2141                                    , 2005,                       ,Pamatpārbaude     ,          0
02.01.2024 ,VW GOLF                                    , 2030,Dīzeļdegviela          ,Pamatpārbaude     ,          2
`

func TestCount(t *testing.T) {
	raw, err := charmap.Windows1257.NewEncoder().String(lines)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCounter(testMakes)
	if err := c.Count(strings.NewReader(raw), 2024); err != nil {
		t.Fatal(err)
	}
	if c.Read != 8 || c.Counted != 4 {
		t.Errorf("read %d, counted %d; want 8, 4", c.Read, c.Counted)
	}
	st := c.Table(2)
	if st.Years != [2]int{2024, 2024} || len(st.Models) != 1 || st.Models[0].Model != "Golf" {
		t.Fatalf("table = %+v", st)
	}
	if s := st.Models[0].Fuels[deals.Diesel]; s.From != 2010 || s.N[0] != 2 || s.Fails[0] != 1 {
		t.Errorf("golf = %+v", s)
	}
	if s := st.Base[deals.Petrol]; s.Total() != 2 {
		t.Errorf("petrol base = %+v, want the Golf Plus and the Audi", s)
	}

	out, err := Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var back deals.InspectionStats
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("%v in\n%s", err, out)
	}
	if back.Source != Source || len(back.Models) != 1 || !bytes.Contains(out, []byte("\n{\"make\":\"Volkswagen\"")) {
		t.Errorf("round trip = %+v\n%s", back, out)
	}
}

func TestFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		_, _ = w.Write([]byte(`{"result":{"resources":[
			{"url":"https://x/download/ta2024_7_12.lst"}, {"url":"https://x/download/ta2021.csv"},
			{"url":"https://x/download/ta2024_1_6.lst"}, {"url":"https://x/download/ta2023.lst"},
			{"url":"https://x/download/ta2022.lst"}, {"url":"https://x/download/readme.pdf"}]}}`))
	}))
	defer srv.Close()
	files, err := Files(context.Background(), srv.Client(), srv.URL, 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.URL[strings.LastIndex(f.URL, "/")+1:])
	}
	if strings.Join(got, " ") != "ta2023.lst ta2024_7_12.lst ta2024_1_6.lst" {
		t.Errorf("files = %v", got)
	}
}
