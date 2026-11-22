package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/notifications"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/telegram"
)

var ad = events.ListingDiscovered{
	Source: "ss.com", ExternalID: "abc", Title: "Skoda  Octavia <RS>", URL: "https://ss.com/a.html?x=1&y=2",
	Price: "6 500 €", ImageURL: "https://i.ss.com/a.jpg",
	Fields: []events.Field{{Name: "🚗 Car", Value: "Skoda Octavia"}, {Name: "📅 Year", Value: "2015"}},
}

func TestListingHTML(t *testing.T) {
	got := listingHTML(ad, "", maxCaption)
	want := "<b>🆕 New listing</b>\nSkoda Octavia &lt;RS&gt;\n💰 <b>6 500 €</b>\n<b>🚗 Car</b>: Skoda Octavia\n<b>📅 Year</b>: 2015\n" +
		`<a href="https://ss.com/a.html?x=1&amp;y=2">📎 View on ss.com</a>` + "\nID: abc"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	// Facts that don't fit the caption are left out; the link stays.
	long := ad
	long.Fields = nil
	for range 40 {
		long.Fields = append(long.Fields, events.Field{Name: "Fact", Value: strings.Repeat("x", 40)})
	}
	got = listingHTML(long, "en", maxCaption)
	visible := strings.NewReplacer("<b>", "", "</b>", "", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(got)
	visible = visible[:strings.Index(visible, "<a href")] + "📎 View on ss.com\nID: abc"
	if n := utf8.RuneCountInString(visible); n > maxCaption || !strings.Contains(got, "View on ss.com") {
		t.Errorf("caption of %d characters:\n%s", n, got)
	}

	lv := ad
	lv.Fields = []events.Field{{Name: "🚗 Car", Names: map[string]string{"lv": "🚗 Auto"}, Value: "Skoda Octavia"}}
	if got := listingHTML(lv, "lv", maxCaption); !strings.HasPrefix(got, "<b>🆕 Jauns sludinājums</b>") ||
		!strings.Contains(got, "<b>🚗 Auto</b>: Skoda Octavia") || !strings.Contains(got, "📎 Skatīt vietnē ss.com") {
		t.Errorf("latvian:\n%s", got)
	}
}

func TestNotifyListing(t *testing.T) {
	var methods []string
	photoFails, blocked := true, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		methods = append(methods, method)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case blocked:
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		case method == "sendPhoto" && photoFails:
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: wrong file identifier/HTTP URL specified"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":5}}}`))
		}
	}))
	defer srv.Close()
	n := New(telegram.New("t").WithBaseURL(srv.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := n.NotifyListing(ctx, "5", ad, ""); err != nil || strings.Join(methods, ",") != "sendPhoto,sendMessage" {
		t.Errorf("a photo Telegram can't fetch falls back to text: %v, %v", methods, err)
	}
	methods, photoFails = nil, false
	if err := n.NotifyListing(ctx, "5", ad, ""); err != nil || strings.Join(methods, ",") != "sendPhoto" {
		t.Errorf("photo: %v, %v", methods, err)
	}
	methods, blocked = nil, true
	if err := n.NotifyListing(ctx, "5", ad, ""); !errors.Is(err, notifications.ErrUndeliverable) || len(methods) != 1 {
		t.Errorf("blocked: %v, %v", methods, err)
	}
	if err := n.NotifyText(ctx, "5", "hi"); !errors.Is(err, notifications.ErrUndeliverable) {
		t.Errorf("blocked text: %v", err)
	}
}
