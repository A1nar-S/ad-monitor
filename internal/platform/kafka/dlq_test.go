package kafka

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestDLQTopic(t *testing.T) {
	if got := DLQTopic("it1.listings.new"); got != "it1.listings.new.dlq" {
		t.Fatalf("DLQTopic = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	long := strings.Repeat("a", 50)
	if got := truncate(long, 20); len(got) > 20 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncate = %q (%d bytes)", got, len(got))
	}
	// A multi-byte character must not be cut in half.
	for max := 5; max < 12; max++ {
		got := truncate(strings.Repeat("ā", 20), max)
		if !utf8.ValidString(got) || len(got) > max {
			t.Errorf("max %d: %q (%d bytes)", max, got, len(got))
		}
	}
}

func TestDeadLetter(t *testing.T) {
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	src := &kgo.Record{
		Topic: "ns.listings.new", Partition: 2, Offset: 41, Key: []byte("ss.com/1"), Value: []byte("{broken"),
		Headers: []kgo.RecordHeader{
			{Key: "trace", Value: []byte("abc")},
			{Key: HeaderReason, Value: []byte("stale")}, // from an earlier round
		},
	}
	got := deadLetter(src, "ns.matcher", ReasonUndecodable, errors.New(strings.Repeat("x", 5000)), at)

	if got.Topic != "ns.listings.new.dlq" || string(got.Key) != "ss.com/1" || string(got.Value) != "{broken" {
		t.Fatalf("record = %s key=%q value=%q", got.Topic, got.Key, got.Value)
	}
	want := map[string]string{
		"trace":             "abc",
		HeaderOriginalTopic: "ns.listings.new",
		HeaderPartition:     "2",
		HeaderOffset:        "41",
		HeaderGroup:         "ns.matcher",
		HeaderFailedAt:      "2026-11-20T12:00:00Z",
		HeaderReason:        ReasonUndecodable,
	}
	for k, v := range want {
		if n := countHeaders(got, k); n != 1 {
			t.Errorf("header %s appears %d times", k, n)
		}
		if h := header(got, k); h != v {
			t.Errorf("header %s = %q, want %q", k, h, v)
		}
	}
	if e := header(got, HeaderError); len(e) == 0 || len(e) > maxErrorLen {
		t.Errorf("error header is %d bytes, want 1..%d", len(e), maxErrorLen)
	}
}

func countHeaders(r *kgo.Record, key string) (n int) {
	for _, h := range r.Headers {
		if h.Key == key {
			n++
		}
	}
	return n
}
