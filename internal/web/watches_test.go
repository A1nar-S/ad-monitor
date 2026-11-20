package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping/pages"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// fakeWatches records what the API hands the subscriptions slice; the
// embedded interface panics for calls a test doesn't expect.
type fakeWatches struct {
	Watches
	created []subscriptions.WatchInput
}

func (*fakeWatches) AllowedWatchIntervals() []int { return []int{60, 1440} }
func (f *fakeWatches) CreateWatch(_ context.Context, _ subscriptions.Owner, in subscriptions.WatchInput) (subscriptions.WatchView, error) {
	f.created = append(f.created, in)
	return subscriptions.WatchView{ID: 5, Name: in.Name, URL: in.URL}, nil
}

// fakePages serves the Strandberg fixture for any https address.
type fakePages struct{ fetched int }

func (f *fakePages) Fetch(_ context.Context, url string) (*pages.Page, error) {
	f.fetched++
	if strings.Contains(url, "127.0.0.1") {
		return nil, &sources.InvalidSearch{Msg: pages.ErrPrivateAddress.Error()}
	}
	body, err := os.ReadFile("../scraping/pages/testdata/strandberg.html")
	return &pages.Page{URL: "https://strandberg.example/boden", Status: 200, ContentType: "text/html", Body: body}, err
}
func (*fakePages) ReadPrice(context.Context, string, sources.Extractor) (sources.Reading, error) {
	return sources.Reading{}, sources.ErrBlocked
}

func newWatchServer(t *testing.T) (http.Handler, *fakeWatches, *fakePages) {
	t.Helper()
	w, p := &fakeWatches{}, &fakePages{}
	return New(Config{Searches: &fakeSearches{}, Listings: fakeListings{}, Watches: w, Pages: p, MaxWatches: 3}), w, p
}

func TestPricePicker(t *testing.T) {
	h, watches, _ := newWatchServer(t)

	w := do(h, "POST", "/api/pages/inspect", "s-alice", `{"url":"https://strandberg.example/boden"}`, true)
	var page struct {
		Snapshot    string
		URL         string
		Kind        string
		Title       string
		Currency    string
		Suggestions []pages.Suggestion
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != http.StatusOK || page.Snapshot == "" {
		t.Fatalf("inspect: %d %s", w.Code, w.Body)
	}
	if page.Kind != "html" || page.Currency != "EUR" || page.Title != "Boden Essential 6 Tremolo Whale Blue" || len(page.Suggestions) != 2 {
		t.Errorf("inspect: %+v", page)
	}
	// One page load per user per moment: the server fetches on their behalf.
	if w := do(h, "POST", "/api/pages/inspect", "s-alice", `{"url":"https://strandberg.example/boden"}`, true); w.Code != http.StatusTooManyRequests {
		t.Errorf("second load at once: %d", w.Code)
	}
	if w := do(h, "POST", "/api/pages/inspect", "s-admin", `{"url":"http://127.0.0.1:5432/"}`, true); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "private network") {
		t.Errorf("private address: %d %s", w.Code, w.Body)
	}

	// The iframe's copy: no session needed, sandboxed, only our script.
	w = do(h, "GET", "/api/pages/snapshots/"+page.Snapshot, "", "", false)
	csp := w.Header().Get("Content-Security-Policy")
	body, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusOK || !strings.HasPrefix(csp, "sandbox allow-scripts;") || !strings.Contains(csp, "script-src 'nonce-") {
		t.Fatalf("snapshot: %d %q", w.Code, csp)
	}
	nonce := strings.TrimSuffix(strings.SplitN(strings.SplitN(csp, "'nonce-", 2)[1], "'", 2)[0], "")
	if !strings.Contains(string(body), `<script nonce="`+nonce+`">`) || strings.Contains(string(body), "main.js") {
		t.Errorf("snapshot body lacks our nonce'd script or keeps the shop's")
	}
	if w := do(h, "GET", "/api/pages/snapshots/nope", "", "", false); w.Code != http.StatusGone {
		t.Errorf("unknown snapshot: %d", w.Code)
	}

	// "The server reads": the same extraction the watch will run.
	test := func(session, extractor string) testView {
		t.Helper()
		w := do(h, "POST", "/api/pages/test", session, `{"snapshot":"`+page.Snapshot+`","extractor":`+extractor+`}`, true)
		var v testView
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		if session == "s-admin" && w.Code != http.StatusGone {
			t.Errorf("another user's copy: %d", w.Code)
		}
		return v
	}
	if v := test("s-alice", `{"selector":"div.product p > span.text-xl"}`); v.Price == nil || v.Price.Cents != 129900 || v.Matches != 1 {
		t.Errorf("test: %+v", v)
	}
	if v := test("s-alice", `{"selector":"span.sale"}`); v.Price != nil || !strings.Contains(v.Error, "nothing on the page matches") {
		t.Errorf("test missing: %+v", v)
	}
	test("s-admin", `{"selector":"span"}`)

	// Saving takes the baseline from the copy the price was picked on.
	w = do(h, "POST", "/api/watches", "s-alice", `{"name":"Boden","url":"https://strandberg.example/boden","snapshot":"`+page.Snapshot+
		`","extractor":{"selector":"div.product p > span.text-xl"},"rule":{"kind":"drop","dropPercent":5},"intervalMinutes":60,"channelId":3}`, true)
	if w.Code != http.StatusCreated || len(watches.created) != 1 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	in := watches.created[0]
	if in.Reading == nil || in.Reading.Price != (sources.Price{Cents: 129900, Currency: "EUR"}) || in.URL != "https://strandberg.example/boden" ||
		in.Reading.Title != "Boden Essential 6 Tremolo Whale Blue" || *in.Rule.DropPercent != 5 {
		t.Errorf("create input: %+v", in)
	}
	// Without a copy, the page is read live; a refusing shop is explained.
	w = do(h, "POST", "/api/watches", "s-alice", `{"name":"B","url":"https://strandberg.example/boden","extractor":{"selector":"span"},"rule":{"kind":"below","targetCents":100},"intervalMinutes":60,"channelId":3}`, true)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "refused") {
		t.Errorf("live read refused: %d %s", w.Code, w.Body)
	}

	w = do(h, "GET", "/api/me", "s-alice", "", false)
	if !strings.Contains(w.Body.String(), `"watchIntervals":[60,1440]`) || !strings.Contains(w.Body.String(), `"maxWatches":3`) {
		t.Errorf("me: %s", w.Body)
	}
}
