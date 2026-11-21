package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

func TestEmbedRepost(t *testing.T) {
	l := events.ListingDiscovered{Source: "ss.com", ExternalID: "2", Title: "BMW 520", Price: "8 500 €"}
	if e := embed(l); e.Color != embedColor || !strings.Contains(e.Title, "New") {
		t.Errorf("new ad: colour %#x title %q, want green and new", e.Color, e.Title)
	}

	l.RepostOf = &events.Repost{ExternalID: "1", FirstListed: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Times: 1}
	e := embed(l)
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
}
