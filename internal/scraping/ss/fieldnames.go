package ss

import "github.com/A1nar-S/advertisiment-monitoring/internal/events"

// lvNames are the alert fields' names in Latvian, by their English ones.
var lvNames = map[string]string{
	"📍 Location": "📍 Atrašanās vieta", "🚗 Car": "🚗 Auto", "📅 Year": "📅 Gads", "⚙️ Engine": "⚙️ Motors",
	"🛣️ Mileage": "🛣️ Nobraukums", "🚪 Rooms": "🚪 Istabas", "📐 Area": "📐 Platība", "🏢 Series": "🏢 Sērija",
	"🕹️ Gearbox": "🕹️ Ātrumkārba", "🚙 Body": "🚙 Virsbūve", "🎨 Color": "🎨 Krāsa",
	"🔧 Inspection": "🔧 Tehniskā apskate", "🏠 House": "🏠 Māja",
}

// field is one alert fact, named in every alert language there is a name
// for. Values stay as ss.com writes them (Latvian).
func field(name, value string, inline bool) events.Field {
	f := events.Field{Name: name, Value: value, Inline: inline}
	if lv, ok := lvNames[name]; ok {
		f.Names = map[string]string{events.LangLatvian: lv}
	}
	return f
}
