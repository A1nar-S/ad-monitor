package rrr

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveAPI checks that rrr.lt's API still accepts the feed query and
// returns what a listing is built from. It makes two real requests, so it
// only runs on request:
//
//	RRR_LIVE=1 go test ./internal/scraping/rrr -run TestLiveAPI -v
//
// A failure means the API changed: compare partsQuery with what the site's
// own pages send, re-save testdata/page_bmw_ac.json and adjust the mapping.
func TestLiveAPI(t *testing.T) {
	if os.Getenv("RRR_LIVE") == "" {
		t.Skip("RRR_LIVE not set")
	}
	c := NewClient(30 * time.Second)
	ctx := context.Background()

	for page := 1; page <= 2; page++ {
		ls, err := c.FetchFeed(ctx, fixtureFeed, page)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(ls) == 0 {
			t.Fatalf("page %d: no parts", page)
		}
		for i, l := range ls {
			if l.URL == "" || l.Title == "" || l.PriceValue == nil {
				t.Errorf("page %d #%d: incomplete listing %+v", page, i, l)
			}
			l.Feed = "" // check by attrs, not by the feed it came from
			if !c.InScope([]string{fixtureFeed}, l) {
				t.Errorf("page %d #%d: %s is not BMW A/C by its attrs: %v", page, i, l.ExternalID, l.Attrs)
			}
			if i > 0 && l.PostedAt.After(ls[i-1].PostedAt) {
				t.Errorf("page %d: not newest first at #%d", page, i)
			}
		}
		time.Sleep(2 * time.Second) // be polite
	}
}
