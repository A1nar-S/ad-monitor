package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain & <simple>", "plain &amp; &lt;simple&gt;"},
		{"✅ Saved **Cheap Skoda** (cars)", "✅ Saved <b>Cheap Skoda</b> (cars)"},
		{"**unclosed", "**unclosed"},
		{`price \*7000\* \[x\] \<y`, "price *7000* [x] &lt;y"},
		{"• [Skoda \\[RS\\]](<https://ss.com/1.html?a=1&b=2>) · 6 500 €",
			`• <a href="https://ss.com/1.html?a=1&amp;b=2">Skoda [RS]</a> · 6 500 €`},
		{"[plain](https://ss.com/x)", `<a href="https://ss.com/x">plain</a>`},
		{"[not a link](javascript:alert(1))", "[not a link](javascript:alert(1))"},
		{"login:\n<https://ads.example/api/login?token=a_b>", "login:\nhttps://ads.example/api/login?token=a_b"},
		{"a <#123> b", "a &lt;#123&gt; b"},
		{"**[bold link](<https://x.lv>)**", `<b><a href="https://x.lv">bold link</a></b>`},
		{"Rīga 'quote' \"dq\"", "Rīga &#39;quote&#39; &#34;dq&#34;"},
	} {
		if got := HTML(tc.in); got != tc.want {
			t.Errorf("HTML(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestUndeliverable(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&Error{Code: 403, Description: "Forbidden: bot was blocked by the user"}, true},
		{&Error{Code: 400, Description: "Bad Request: chat not found"}, true},
		{&Error{Code: 400, Description: "Bad Request: wrong file identifier/HTTP URL specified"}, false},
		{&Error{Code: 429, Description: "Too Many Requests"}, false},
		{errors.New("timeout"), false},
		{nil, false},
	} {
		if got := Undeliverable(tc.err); got != tc.want {
			t.Errorf("%v: %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestClient(t *testing.T) {
	var calls []string
	limited := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage") && limited:
			limited = false
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 0","parameters":{"retry_after":0}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			if body["chat_id"] != float64(42) || body["parse_mode"] != "HTML" {
				t.Errorf("body = %v", body)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7,"chat":{"id":42,"type":"private"}}}`))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		}
	}))
	defer srv.Close()
	c := New("123:secret").WithBaseURL(srv.URL)
	ctx := context.Background()

	m, err := c.SendMessage(ctx, "42", "<b>hi</b>", nil, false)
	if err != nil || m.MessageID != 7 {
		t.Fatalf("send: %+v, %v", m, err)
	}
	if len(calls) != 2 || calls[0] != "/bot123:secret/sendMessage" {
		t.Errorf("a 429 is retried once: %v", calls)
	}

	err = c.SendPhoto(ctx, "42", "https://x/y.jpg", "c")
	var te *Error
	if !errors.As(err, &te) || te.Code != 403 || !Undeliverable(err) {
		t.Errorf("refused: %v", err)
	}

	// A network error must not repeat the URL, which holds the token.
	srv.Close()
	if err := c.SendPhoto(ctx, "42", "https://x/y.jpg", "c"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("network error = %v", err)
	}
}
