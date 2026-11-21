package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// fakeSite serves two feeds; the ad "shared" is on both.
type fakeSite struct {
	fetched []string
	status  int // non-zero: every fetch is refused with it
}

type refused int

func (r refused) Error() string   { return fmt.Sprintf("status %d", int(r)) }
func (r refused) HTTPStatus() int { return int(r) }

func (*fakeSite) Name() string { return "fake" }
func (f *fakeSite) FetchFeed(_ context.Context, feed string, page int) ([]events.ListingDiscovered, error) {
	f.fetched = append(f.fetched, fmt.Sprintf("%s#%d", feed, page))
	if f.status != 0 {
		return nil, refused(f.status)
	}
	at := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	if feed == "b" {
		at = at.Add(time.Hour) // newer
	}
	return []events.ListingDiscovered{
		{ExternalID: feed + "1", Title: "Turbo " + feed, URL: "https://site.example/" + feed, PostedAt: at},
		{ExternalID: "shared", Title: "Shared", PostedAt: at.Add(-time.Minute)},
	}, nil
}
func (*fakeSite) PlanFeeds(w []string, _ int) []string                { return w }
func (*fakeSite) InScope(_ []string, _ events.ListingDiscovered) bool { return true }

func liveServer(t *testing.T, site *fakeSite) (http.Handler, *fakeSearches) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeSearches{enabled: map[int64]bool{}}
	return New(Config{Searches: f, Listings: fakeListings{}, Sources: sources.NewRegistry(site), WebDir: dir}), f
}

func TestPreviewLive(t *testing.T) {
	site := &fakeSite{}
	h, f := liveServer(t, site)
	var got []events.ListingDiscovered
	f.previewOf = func(c []events.ListingDiscovered) subscriptions.Preview {
		got = c
		return subscriptions.Preview{InScope: len(c), Matched: []int{0, 2}, Unsure: []int{1}, UnsureKeys: []string{"gearbox"}}
	}
	body := `{"source":"fake","category":"parts","filters":{"make":["BMW"]}}`

	w := do(h, "POST", "/api/searches/preview/live", "s-alice", body, true)
	if w.Code != http.StatusOK {
		t.Fatalf("live preview: %d %s", w.Code, w.Body)
	}
	if strings.Join(site.fetched, " ") != "a#1 b#1" {
		t.Errorf("fetched %v, want page 1 of each feed", site.fetched)
	}
	// Deduplicated, newest first, each with the source and feed it came from.
	var ids []string
	for _, l := range got {
		ids = append(ids, l.ExternalID+"@"+l.Feed)
		if l.Source != "fake" {
			t.Errorf("%s: source %q", l.ExternalID, l.Source)
		}
	}
	if strings.Join(ids, " ") != "b1@b a1@a shared@a" {
		t.Errorf("candidates = %v", ids)
	}
	var v previewView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Live || v.Scanned != 3 || v.Matched != 2 || v.Unsure != 1 || len(v.Ads) != 3 ||
		v.Ads[0].Title != "Turbo b" || !v.Ads[0].Sure || v.Ads[1].Sure || v.Ads[0].ID == v.Ads[2].ID {
		t.Errorf("view = %+v", v)
	}
	if p := f.previewed[len(f.previewed)-1]; p.Source != "fake" || p.Category != "parts" {
		t.Errorf("previewed %+v", p)
	}

	// One press per user per liveGap, and one per site per liveSiteGap
	// whoever presses: neither refused press reaches the site.
	if w := do(h, "POST", "/api/searches/preview/live", "s-alice", body, true); w.Code != http.StatusTooManyRequests {
		t.Errorf("second press: %d, want 429", w.Code)
	}
	w = do(h, "POST", "/api/searches/preview/live", "s-admin", body, true)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "Someone just checked fake") {
		t.Errorf("another user at once: %d %s, want the site's 429", w.Code, w.Body)
	}
	if len(site.fetched) != 2 {
		t.Errorf("fetched %v: refused presses must not reach the site", site.fetched)
	}
}

func TestLiveLimiter(t *testing.T) {
	var l liveLimiter
	now := time.Now()
	for _, c := range []struct {
		user             int64
		source           string
		after            time.Duration
		wantOK, wantBusy bool
	}{
		{1, "rrr.lt", 0, true, false},
		{1, "ss.com", time.Second, false, false}, // the same user, too soon
		{2, "rrr.lt", time.Second, false, true},  // the same site, too soon
		{2, "ss.com", time.Second, true, false},  // another user and site
		{2, "rrr.lt", liveGap, false, false},     // user 2 pressed at 1s
		{3, "rrr.lt", liveSiteGap + time.Second, true, false},
		{1, "rrr.lt", liveGap + liveSiteGap + time.Second, true, false},
	} {
		ok, busy := l.allow(c.user, c.source, now.Add(c.after))
		if ok != c.wantOK || busy != c.wantBusy {
			t.Errorf("user %d %s at +%v: ok=%v busy=%v, want %v %v", c.user, c.source, c.after, ok, busy, c.wantOK, c.wantBusy)
		}
	}
}

func TestPreviewLiveErrors(t *testing.T) {
	site := &fakeSite{status: http.StatusForbidden}
	h, _ := liveServer(t, site)

	// A mistake in the search costs no request and no press.
	w := do(h, "POST", "/api/searches/preview/live", "s-alice", `{"source":"fake","category":"boats"}`, true)
	if w.Code != http.StatusUnprocessableEntity || len(site.fetched) != 0 {
		t.Errorf("invalid search: %d %s, fetched %v", w.Code, w.Body, site.fetched)
	}
	w = do(h, "POST", "/api/searches/preview/live", "s-alice", `{"source":"fake","category":"parts"}`, true)
	msg, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusBadGateway || !strings.Contains(string(msg), "refused the request (status 403)") {
		t.Errorf("site refused: %d %s", w.Code, msg)
	}
	if w := do(h, "POST", "/api/searches/preview/live", "s-alice", `{"source":"fake","category":"parts"}`, false); w.Code != http.StatusForbidden {
		t.Errorf("without the CSRF header: %d", w.Code)
	}
}
