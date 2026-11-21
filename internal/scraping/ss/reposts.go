package ss

import (
	"strconv"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// fingerprint identifies the item behind an ad, so a deleted and reposted ad
// (new ID) is recognised; see events.ListingDiscovered.Fingerprint. It
// includes the feed's deal: the same flat for rent and for sale are two
// offers, not a repost. "" when the list row says too little to tell items
// apart:
//
//   - cars: make, model, year, engine and fuel, and the mileage, which is
//     what tells a dealer's identical cars apart, so it is required;
//   - flats: location, street, rooms, area, floor (of how many) and series;
//     rooms, area and floor are required.
func fingerprint(l events.ListingDiscovered, deal string) string {
	num := func(key string) string {
		if v, ok := l.Num(key); ok && v > 0 {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	str := func(key string) string {
		v, _ := l.Str(key)
		return strings.ToLower(strings.Join(strings.Fields(v), " "))
	}
	var parts []string
	switch {
	case strings.HasPrefix(l.Location, CarsCategory+"/"):
		parts = []string{"car", deal, str("make"), str("model"), num("year"), num("engine_l"), str("fuel"), num("mileage_km")}
		if parts[2] == "" || parts[3] == "" || parts[4] == "" || parts[7] == "" {
			return ""
		}
	case strings.HasPrefix(l.Location, "real-estate/flats/"):
		parts = []string{"flat", deal, l.Location, str("address"), num("rooms"), num("area_m2"), num("floor"), num("floors"), str("series")}
		if parts[4] == "" || parts[5] == "" || parts[6] == "" {
			return ""
		}
	default:
		return ""
	}
	return strings.Join(parts, "|")
}
