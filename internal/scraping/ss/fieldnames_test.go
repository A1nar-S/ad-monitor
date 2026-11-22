package ss

import "testing"

// Every fact an ss.com alert shows has its Latvian name, also those only an
// ad page adds.
func TestFieldsNamedInLatvian(t *testing.T) {
	for _, file := range []string{"testdata/feed_district.html", "testdata/feed_cars_model.html", "testdata/feed_cars_all.html"} {
		for _, l := range parseFile(t, file) {
			for _, f := range l.Fields {
				if f.NameIn("lv") == f.Name {
					t.Errorf("%s: %q has no Latvian name", file, f.Name)
				}
			}
		}
	}
	for _, name := range []string{"📍 Location", "🕹️ Gearbox", "🚙 Body", "🎨 Color", "🔧 Inspection", "🏠 House"} {
		if field(name, "x", true).NameIn("lv") == name {
			t.Errorf("ad-page fact %q has no Latvian name", name)
		}
	}
}
