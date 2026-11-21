package ss

import (
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

func TestFingerprint(t *testing.T) {
	car := func(attrs map[string]any) events.ListingDiscovered {
		return events.ListingDiscovered{Location: "transport/cars/bmw/520", Attrs: attrs}
	}
	full := map[string]any{"make": "BMW", "model": "520", "year": 2016.0, "engine_l": 2.0, "fuel": "Dīzelis", "mileage_km": 189000.0}
	if got, want := fingerprint(car(full), "sell"), "car|sell|bmw|520|2016|2|dīzelis|189000"; got != want {
		t.Errorf("car = %q, want %q", got, want)
	}
	noKm := map[string]any{"make": "BMW", "model": "520", "year": 2016.0, "engine_l": 2.0, "fuel": "Dīzelis"}
	if got := fingerprint(car(noKm), "sell"); got != "" {
		t.Errorf("car without mileage = %q, want none: a dealer's identical cars would collide", got)
	}
	if fingerprint(car(full), "sell") == fingerprint(car(full), "buy") {
		t.Error("a sale and a wanted ad share a fingerprint")
	}

	flat := events.ListingDiscovered{Location: "real-estate/flats/riga/centre", Attrs: map[string]any{
		"address": "Brīvības  iela 10", "rooms": 2.0, "area_m2": 54.0, "floor": 3.0, "floors": 5.0, "series": "Staļina",
	}}
	if got, want := fingerprint(flat, "hand_over"), "flat|hand_over|real-estate/flats/riga/centre|brīvības iela 10|2|54|3|5|staļina"; got != want {
		t.Errorf("flat = %q, want %q", got, want)
	}
	delete(flat.Attrs, "floor")
	if got := fingerprint(flat, "hand_over"); got != "" {
		t.Errorf("flat without floor = %q, want none", got)
	}
	if got := fingerprint(events.ListingDiscovered{Location: "electronics/phones"}, "sell"); got != "" {
		t.Errorf("other category = %q, want none", got)
	}
}
