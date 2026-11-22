package matching

import (
	"context"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

func ptr[T any](v T) *T { return &v }

// fakeSource scopes by plain prefix; its catalog takes the category as the
// feed and the filters as they are.
type fakeSource struct{}

func (fakeSource) Name() string { return "ss.com" }
func (fakeSource) FetchFeed(context.Context, string, int) ([]events.ListingDiscovered, error) {
	return nil, nil
}
func (fakeSource) PlanFeeds(wanted []string, _ int) []string { return wanted }
func (fakeSource) InScope(wanted []string, l events.ListingDiscovered) bool {
	return slices.ContainsFunc(wanted, func(w string) bool { return strings.HasPrefix(l.Location, w) })
}
func (fakeSource) Categories() []sources.Category { return []sources.Category{{ID: "cars"}} }
func (fakeSource) CompileSearch(category string, filters map[string]any) (sources.Compiled, error) {
	return sources.Compiled{Spec: filters, Feeds: []string{category}, Filters: filters}, nil
}

// enrichingSource reads "ad pages" by counting calls.
type enrichingSource struct {
	fakeSource
	calls int
	err   error
}

func (*enrichingSource) EnrichableKeys() []string { return []string{"gearbox"} }
func (e *enrichingSource) Enrich(_ context.Context, l events.ListingDiscovered) (events.ListingDiscovered, error) {
	e.calls++
	if e.err != nil {
		return l, e.err
	}
	out := l
	out.Attrs = maps.Clone(l.Attrs)
	if out.Attrs == nil {
		out.Attrs = map[string]any{}
	}
	out.Attrs["gearbox"] = "Automāts"
	return out, nil
}

// Needs a real Postgres: TEST_DATABASE_URL=postgres://... go test ./internal/matching
// The searches come from a real subscriptions slice in the same database.
func newTestServices(t *testing.T, src sources.Source) (*Service, *subscriptions.Service) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS link_codes, accounts, watch_alerts, watch_prices, watches, watch_targets, settings, sessions, login_tokens, matches, searches, channels, users, subscriptions_migrations, matching_migrations`); err != nil {
		t.Fatal(err)
	}
	reg := sources.NewRegistry(src)
	subs := subscriptions.NewService(pool, reg, 3)
	svc := NewService(pool, subs, reg)
	subs.WithMatchLog(svc)
	for range 2 { // migrations are idempotent
		for _, m := range []func(context.Context) error{subs.Migrate, svc.Migrate} {
			if err := m(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := svc.AwaitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return svc, subs
}

// user makes a Discord user with a DM channel.
func user(t *testing.T, subs *subscriptions.Service, discordID string) (subscriptions.Owner, subscriptions.ChannelView) {
	t.Helper()
	ctx := context.Background()
	o, err := subs.PlatformOwner(ctx, "discord", discordID, "name-"+discordID)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := subs.EnsureChannel(ctx, o, "discord", "dm-"+discordID, "Private messages")
	if err != nil {
		t.Fatal(err)
	}
	return o, ch
}

func search(t *testing.T, subs *subscriptions.Service, o subscriptions.Owner, ch subscriptions.ChannelView, name string, filters map[string]any) subscriptions.SearchView {
	t.Helper()
	v, err := subs.CreateSearch(context.Background(), o, subscriptions.SearchInput{Name: name, Category: "cars", ChannelID: ch.ID, Filters: filters})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMatchRecordAndRecent(t *testing.T) {
	svc, subs := newTestServices(t, fakeSource{})
	ctx := context.Background()
	alice, aliceDM := user(t, subs, "111")
	v := search(t, subs, alice, aliceDM, "cheap", map[string]any{"price": map[string]any{"max": 450.0}})

	l := events.ListingDiscovered{Source: "ss.com", ExternalID: "1", Location: "cars", PriceValue: ptr(400.0)}
	var sent []events.NotificationRequested
	publish := func(_ context.Context, n events.NotificationRequested) error {
		sent = append(sent, n)
		return nil
	}
	if err := svc.Handle(ctx, l, publish); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Channel.Target != "dm-111" || sent[0].SearchName != "cheap" || !strings.HasSuffix(sent[0].Key, ":ss.com:1") {
		t.Fatalf("sent = %+v", sent)
	}
	// A redelivered listing is told to no one again.
	if err := svc.Handle(ctx, l, publish); err != nil || len(sent) != 1 {
		t.Errorf("redelivery: %d sent (err %v), want still 1", len(sent), err)
	}
	if sent[0].Lang != "lv" {
		t.Errorf("lang = %q, want the default lv", sent[0].Lang)
	}
	// A failed publish records nothing, so the retry tells them.
	l.ExternalID = "2"
	if err := svc.Handle(ctx, l, func(context.Context, events.NotificationRequested) error { return errors.New("down") }); err == nil {
		t.Fatal("a failed publish was not reported")
	}
	if err := svc.Handle(ctx, l, publish); err != nil || len(sent) != 2 {
		t.Errorf("retry: %d sent (err %v), want 2", len(sent), err)
	}

	// The searches' views read the log.
	if ms, err := subs.RecentMatches(ctx, alice, 0, 10); err != nil || len(ms) != 2 || ms[0].ExternalID != "2" || ms[0].SearchName != "cheap" {
		t.Errorf("recent = %+v, %v", ms, err)
	}
	if got, _ := subs.GetSearch(ctx, alice, v.ID); got.Matches != 2 {
		t.Errorf("search shows %d matches, want 2", got.Matches)
	}
	if n, err := svc.Purge(ctx, 0); err != nil || n != 2 {
		t.Errorf("purged %d (err %v), want 2", n, err)
	}
}

func TestNotificationsEnrichOnce(t *testing.T) {
	src := &enrichingSource{}
	svc, subs := newTestServices(t, src)
	ctx := context.Background()
	alice, aliceDM := user(t, subs, "111")
	bob, bobDM := user(t, subs, "222")
	// Two users, four searches; three need the gearbox, one wants manual.
	search(t, subs, alice, aliceDM, "auto", map[string]any{"gearbox": []any{"Automāts"}})
	search(t, subs, alice, aliceDM, "cheap", map[string]any{"price": map[string]any{"max": 6000.0}})
	search(t, subs, bob, bobDM, "auto-or-manual", map[string]any{"gearbox": []any{"Automāts", "Manuāla"}, "year": map[string]any{"min": 2010.0}})
	search(t, subs, bob, bobDM, "manual", map[string]any{"gearbox": []any{"Manuāla"}})

	var kept []events.AdPageRead
	svc.WithAdPageHook(func(_ context.Context, r events.AdPageRead) { kept = append(kept, r) })

	l := events.ListingDiscovered{Source: "ss.com", ExternalID: "7", Location: "cars", PriceValue: ptr(5000.0), Attrs: map[string]any{"year": 2015.0}}
	got, err := svc.Notifications(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range got {
		names = append(names, n.SearchName)
		if v, _ := n.Listing.Str("gearbox"); v != "Automāts" {
			t.Errorf("%s: notification lacks the enriched gearbox", n.SearchName)
		}
	}
	slices.Sort(names)
	if !reflect.DeepEqual(names, []string{"auto", "auto-or-manual", "cheap"}) || src.calls != 1 {
		t.Errorf("matched %v with %d ad-page reads, want 3 searches and 1 read", names, src.calls)
	}
	if len(kept) != 1 || kept[0].Read == nil || kept[0].Listing.ExternalID != "7" {
		t.Errorf("ad page handed on = %+v, want the one read", kept)
	}

	// The ad page failing: candidates still hear about it, flagged, each
	// in their language (alice chose English, bob reads the default).
	if err := subs.SetAlertLang(ctx, alice, "en"); err != nil {
		t.Fatal(err)
	}
	src.err = errors.New("403")
	l.ExternalID = "8"
	got, err = svc.Notifications(ctx, l)
	if err != nil || len(got) != 4 {
		t.Fatalf("unconfirmed: %d notifications, err %v; want all 4", len(got), err)
	}
	for _, n := range got {
		f := n.Listing.Fields
		want := "⚠️ Unconfirmed"
		if n.UserID == bob.UserID {
			want = "⚠️ Nav pārbaudīts"
		}
		if len(f) == 0 || f[len(f)-1].Name != want || !strings.Contains(f[len(f)-1].Value, "gearbox") || (n.Lang == "lv") != (n.UserID == bob.UserID) {
			t.Errorf("%s: unconfirmed flag %+v, lang %q; want %q", n.SearchName, f, n.Lang, want)
		}
	}

	// A removed ad is announced to no one, also not to the search that
	// matched without its page; ingest hears it is gone.
	src.err = sources.ErrGone
	l.ExternalID = "9"
	if got, _ = svc.Notifications(ctx, l); len(got) != 0 {
		t.Errorf("gone: %+v, want none", got)
	}
	if last := kept[len(kept)-1]; !last.Gone || last.Listing.ExternalID != "9" {
		t.Errorf("last ad page handed on = %+v, want 9 gone", last)
	}
}

// WithAdPageFor reads the page of a matched ad no filter needed it for,
// and an ad that turns out removed is announced to no one.
func TestNotificationsAdPageFor(t *testing.T) {
	src := &enrichingSource{}
	svc, subs := newTestServices(t, src)
	svc.WithAdPageFor(func(l events.ListingDiscovered) bool { return l.Location == "cars" })
	ctx := context.Background()
	alice, aliceDM := user(t, subs, "111")
	search(t, subs, alice, aliceDM, "cheap", map[string]any{"price": map[string]any{"max": 6000.0}})
	l := events.ListingDiscovered{Source: "ss.com", ExternalID: "1", Location: "cars", PriceValue: ptr(5000.0)}

	got, err := svc.Notifications(ctx, l)
	if err != nil || len(got) != 1 || src.calls != 1 {
		t.Fatalf("%d notifications, %d reads, err %v; want 1 and 1", len(got), src.calls, err)
	}
	if v, _ := got[0].Listing.Str("gearbox"); v != "Automāts" {
		t.Error("notification lacks what the page added")
	}

	src.err = errors.New("403")
	l.ExternalID = "2"
	if got, _ := svc.Notifications(ctx, l); len(got) != 1 || len(got[0].Listing.Fields) != 0 {
		t.Errorf("page failed: %+v, want the alert as it was, unflagged", got)
	}

	src.err = sources.ErrGone
	l.ExternalID = "3"
	if got, _ := svc.Notifications(ctx, l); len(got) != 0 {
		t.Errorf("removed ad announced: %+v", got)
	}
}
