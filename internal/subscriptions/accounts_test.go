package subscriptions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLinkAccounts(t *testing.T) {
	svc, pool := newTestService(t, fakeSource{})
	ctx := context.Background()

	// A Discord admin, and the same person on Telegram, not linked yet.
	disc, discDM := user(t, svc, "admin-1")
	tg, err := svc.PlatformOwner(ctx, "telegram", "555", "Alice TG")
	if err != nil || tg.UserID == disc.UserID || tg.Admin {
		t.Fatalf("telegram user: %+v, %v", tg, err)
	}
	tgDM, _ := svc.EnsureChannel(ctx, tg, "telegram", "555", "Private messages (telegram)")
	// Both happen to know the same Discord channel.
	discGroup, _ := svc.EnsureChannel(ctx, disc, "discord", "grp", "#cars")
	tgGroup, _ := svc.EnsureChannel(ctx, tg, "discord", "grp", "#cars")

	mk := func(o Owner, name string, ch ChannelView) {
		t.Helper()
		if _, err := svc.CreateSearch(ctx, o, SearchInput{Name: name, Category: "flats/centre", ChannelID: ch.ID}); err != nil {
			t.Fatal(err)
		}
	}
	mk(disc, "cheap", discDM)
	mk(tg, "cheap", tgDM) // clashes
	mk(tg, "group", tgGroup)
	tgLogin, _ := svc.NewLoginToken(ctx, tg)
	tgSession, _, err := svc.Login(ctx, tgLogin)
	if err != nil {
		t.Fatal(err)
	}

	code, err := svc.NewLinkCode(ctx, disc)
	if err != nil || len(code) != 9 || code[4] != '-' {
		t.Fatalf("code %q, %v", code, err)
	}
	// Typed sloppily on the other app. Looking first shows whose it is and
	// keeps the code.
	if as, err := svc.LinkTarget(ctx, tg, strings.ToLower(code)); err != nil || len(as) != 1 || as[0].Platform != "discord" {
		t.Fatalf("link target: %+v, %v", as, err)
	}
	res, err := svc.Link(ctx, tg, strings.ToLower(strings.Replace(code, "-", " ", 1)))
	if err != nil || res.Searches != 2 || res.Watches != 0 || len(res.Accounts) != 2 {
		t.Fatalf("link: %+v, %v", res, err)
	}

	// Telegram now acts as the Discord admin's user, with the admin's rights.
	again, err := svc.PlatformOwner(ctx, "telegram", "555", "Alice TG")
	if err != nil || again.UserID != disc.UserID || !again.Admin || again.Platform != "telegram" {
		t.Errorf("telegram after linking: %+v, %v", again, err)
	}
	vs, _ := svc.ListSearches(ctx, disc, false)
	var names []string
	for _, v := range vs {
		names = append(names, v.Name)
		if v.Name == "group" && v.Channel.ID != discGroup.ID {
			t.Errorf("the shared channel was not merged: %+v", v.Channel)
		}
	}
	slices.Sort(names)
	if len(names) != 3 || names[0] != "cheap" || !strings.HasPrefix(names[1], "cheap (") || names[2] != "group" {
		t.Errorf("searches = %v", names)
	}
	if chs, _ := svc.Channels(ctx, disc); len(chs) != 3 {
		t.Errorf("channels = %+v, want the DM of each and one group", chs)
	}
	if o, err := svc.SessionOwner(ctx, tgSession); err != nil || o.UserID != disc.UserID {
		t.Errorf("the Telegram dashboard session: %+v, %v", o, err)
	}
	var users int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users)
	if users != 1 {
		t.Errorf("%d users left, want 1", users)
	}

	// Codes are single use, expire, and linking to oneself says so.
	if _, err := svc.Link(ctx, tg, code); !errors.Is(err, ErrBadToken) {
		t.Errorf("reused code: %v", err)
	}
	own, _ := svc.NewLinkCode(ctx, again)
	if _, err := svc.LinkTarget(ctx, disc, own); !errors.Is(err, ErrSameUser) {
		t.Errorf("own code, looked at: %v", err)
	}
	if _, err := svc.Link(ctx, disc, own); !errors.Is(err, ErrSameUser) {
		t.Errorf("own code: %v", err)
	}

	// A new code replaces the user's earlier one.
	first, _ := svc.NewLinkCode(ctx, disc)
	second, _ := svc.NewLinkCode(ctx, disc)
	if _, err := svc.LinkTarget(ctx, tg, first); !errors.Is(err, ErrBadToken) {
		t.Errorf("replaced code: %v", err)
	}
	if _, err := svc.LinkTarget(ctx, again, second); !errors.Is(err, ErrSameUser) {
		t.Errorf("newest code: %v", err)
	}
	late, _ := svc.NewLinkCode(ctx, disc)
	_, _ = pool.Exec(ctx, `UPDATE link_codes SET expires_at = now() - interval '1 minute'`)
	if _, err := svc.Link(ctx, tg, late); !errors.Is(err, ErrBadToken) {
		t.Errorf("expired code: %v", err)
	}
	if err := svc.PurgeExpiredAuth(ctx); err != nil {
		t.Fatal(err)
	}
	var codes int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM link_codes`).Scan(&codes)
	if codes != 0 {
		t.Errorf("%d expired link codes left after the purge", codes)
	}
}

func TestLinkCodeAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for range 200 {
		code, err := newLinkCode()
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range code {
			if i == 4 {
				continue
			}
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("%q has %q", code, r)
			}
			seen[r] = true
		}
	}
	if len(seen) != len(codeAlphabet) {
		t.Errorf("200 codes used %d of %d characters", len(seen), len(codeAlphabet))
	}
}
