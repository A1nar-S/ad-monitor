package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

func TestEmbedRepost(t *testing.T) {
	l := events.ListingDiscovered{Source: "ss.com", ExternalID: "2", Title: "BMW 520", Price: "8 500 €"}
	if e := embed(l, ""); e.Color != embedColor || !strings.Contains(e.Title, "New") {
		t.Errorf("new ad: colour %#x title %q, want green and new", e.Color, e.Title)
	}

	l.RepostOf = &events.Repost{ExternalID: "1", FirstListed: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Times: 1}
	e := embed(l, "en")
	if e.Color != repostColor || e.Title != "♻️ Reposted listing" {
		t.Errorf("repost: colour %#x title %q, want orange and reposted", e.Color, e.Title)
	}
	var seen string
	for _, f := range e.Fields {
		if f.Name == "♻️ Seen before" {
			seen = f.Value
		}
	}
	if !strings.HasPrefix(seen, "First listed 3 Oct 2026") {
		t.Errorf("seen-before field = %q", seen)
	}

	// In Latvian: the embed's own words and the source's field names.
	l.Fields = []events.Field{{Name: "🔧 Part", Names: map[string]string{"lv": "🔧 Detaļa"}, Value: "Spogulis"}}
	e = embed(l, "lv")
	if e.Title != "♻️ Atkārtots sludinājums" || e.Fields[0].Name != "💰 Cena" || e.Fields[2].Name != "🔧 Detaļa" ||
		!strings.HasPrefix(e.Fields[1].Value, "Pirmoreiz ievietots 03.10.2026.") {
		t.Errorf("latvian embed: %q, fields %+v", e.Title, e.Fields)
	}
}
