package rrr

import "github.com/A1nar-S/advertisiment-monitoring/internal/events"

// lvNames are the alert fields' names in Latvian, by their English ones.
var lvNames = map[string]string{
	"🔧 Part": "🔧 Detaļa", "🚗 Car": "🚗 Auto", "⚙️ Engine": "⚙️ Motors", "🕹️ Gearbox": "🕹️ Ātrumkārba",
	"↔️ Side": "↔️ Puse", "✨ Condition": "✨ Stāvoklis", "🔢 Part number": "🔢 Detaļas numurs",
}

// field is one alert fact, named in every alert language there is a name
// for.
func field(name, value string, inline bool) events.Field {
	f := events.Field{Name: name, Value: value, Inline: inline}
	if lv, ok := lvNames[name]; ok {
		f.Names = map[string]string{events.LangLatvian: lv}
	}
	return f
}
