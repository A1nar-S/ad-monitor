package ss

import (
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// otherModel is the codebook's catch-all model ("other"): an ad under it
// could be any car of the make, so it has nothing to be compared with.
const otherModel = "Citi"

// RateableCar implements sources.CarAds: an ad under transport/cars found on
// a "sell" feed, and not filed under the catch-all model. Buy and exchange
// ads have prices too, but not ones to compare a sale with.
func (*Client) RateableCar(l events.ListingDiscovered) bool {
	if model, _ := l.Str("model"); strings.EqualFold(model, otherModel) {
		return false
	}
	return strings.HasPrefix(l.Location, CarsCategory+"/") && parseFeed(l.Feed).deal == "sell"
}

// CarSaleFeed implements sources.CarScan: every car for sale.
func (*Client) CarSaleFeed() string { return CarsCategory + "/sell" }

// carTerms translates the codebook's names (the search form's) for the car
// attrs internal/deals rates into its English vocabulary. Equipment that
// says nothing about how well a car is specced (power steering, spoilers,
// roof rails, ...) is left out and so ignored.
var carTerms = map[string]map[string]string{
	"fuel": {
		"Benzīns":      "petrol",
		"Benzīns/gāze": "petrol", // the list page cannot tell it from petrol
		"Dīzelis":      "diesel",
		"Hibrīds":      "hybrid",
		"Elektriskais": "electric",
	},
	"gearbox": {
		"Automāts": "automatic",
		"Manuāla":  "manual",
	},
	"inspection": {
		"Jā":  "valid",
		"Nav": "none",
	},
	"body": {
		"Sedans":        "sedan",
		"Universāls":    "wagon",
		"Hečbeks":       "hatchback",
		"Apvidus":       "suv",
		"Kupeja":        "coupe",
		"Kabriolets":    "convertible",
		"Minivens":      "minivan",
		"Mikroautobuss": "minibus",
		"Pikaps":        "pickup",
	},
	"equipment": {
		"Klimata kontrole":            "climate control",
		"Kondicionieris":              "air conditioning",
		"Adaptīvā kruīzkontrole":      "adaptive cruise control",
		"Kruīza kontrole":             "cruise control",
		"Autonomais sildītājs":        "auxiliary heater",
		"Pilnpiedziņa 4x4":            "4x4",
		"Panorāmas redzam. kameras":   "360° cameras",
		"Atpakaļskata kamera":         "reversing camera",
		"Parkošanās sens. aizmugurē":  "rear parking sensors",
		"Parkošanās sens. priekšā":    "front parking sensors",
		"Automātiska parkošana":       "self-parking",
		"Panorāmas lūka":              "panoramic roof",
		"Lūka":                        "sunroof",
		"Keyless Go":                  "keyless entry",
		"Lietus sensors":              "rain sensor",
		"Xenona":                      "xenon lights",
		"Vieglmetāla diski":           "alloy wheels",
		"Pneimopiekare":               "air suspension",
		"El. bagāžnieka aizvēršana":   "electric tailgate",
		"Palīgsistēma braukš. joslās": "lane assist",
		"Avārijas bremzēšanas sist.":  "emergency braking",
		"Klusuma zonu asistents":      "blind-spot assist",
		"Ceļa zīmju atpazīš. sistēma": "sign recognition",
		"Distances kontrole":          "distance warning",
		"Nakts redzamības kamera":     "night vision",
		"Sporta pakete":               "sport package",
		"Sakabes āķis":                "tow hook",
	},
}

// CarTerm implements sources.CarAds.
func (*Client) CarTerm(key, value string) string { return carTerms[key][value] }
