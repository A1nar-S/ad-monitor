//go:build integration

package app

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/config"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/kafka"
)

// readAll reads every record currently in topic, from the start.
func readAll(t *testing.T, brokers []string, topic string, want int) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var out []*kgo.Record
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for len(out) < want {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			t.Fatalf("read %s: got %d records, want %d", topic, len(out), want)
		}
		fetches.EachRecord(func(r *kgo.Record) { out = append(out, r) })
	}
	// Anything beyond want would show up on a second short poll.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	cl.PollFetches(ctx2).EachRecord(func(r *kgo.Record) { out = append(out, r) })
	return out
}

func headerOf(r *kgo.Record, key string) string {
	for _, h := range r.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestDeadLetters(t *testing.T) {
	in := startInfra(t)
	ctx := context.Background()
	common := config.Common{KafkaBrokers: in.brokers, Namespace: fmt.Sprintf("it%d", rand.Uint32())}
	topic := common.Topic(events.TopicListingsNew)
	group := common.Group("dlq-test")
	dlqTopic := kafka.DLQTopic(topic)

	// Nothing dead-lettered yet, and not even a topic: listing is empty, not an error.
	if items, total, err := kafka.ListDeadLetters(ctx, in.brokers, topic, DLQReplayGroup(common, events.TopicListingsNew), 10); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("empty list = %v, %d, %v", items, total, err)
	}

	producer, err := kgo.NewClient(kgo.SeedBrokers(in.brokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	send := func(key, value string) {
		rec := &kgo.Record{Topic: topic, Key: []byte(key), Value: []byte(value),
			Headers: []kgo.RecordHeader{{Key: "trace", Value: []byte("t-" + key)}}}
		if err := producer.ProduceSync(ctx, rec).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	listing := func(id string) string { return fmt.Sprintf(`{"source":"ss.com","external_id":%q}`, id) }
	send("good0", listing("good0"))
	send("poison", listing("poison"))
	send("garbage", "this is not json")
	send("good1", listing("good1"))

	var mu sync.Mutex
	var handled []string
	attempts := 0
	handle := func(_ context.Context, l events.ListingDiscovered) error {
		mu.Lock()
		defer mu.Unlock()
		if l.ExternalID == "poison" {
			attempts++
			return fmt.Errorf("cannot handle %s", l.ExternalID)
		}
		handled = append(handled, l.ExternalID)
		return nil
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(handled)
	}
	sub := kafka.Subscription{Brokers: in.brokers, Group: group, Topic: topic, RetryDelay: 10 * time.Millisecond}
	stop := run(t, "consumer", func(ctx context.Context) error { return kafka.Consume(ctx, sub, handle) })

	// The consumer moves past both bad records.
	eventually(t, 60*time.Second, "good records handled", func() bool { return len(snapshot()) == 2 })
	if got := snapshot(); !slices.Equal(got, []string{"good0", "good1"}) {
		t.Fatalf("handled %v", got)
	}

	// Both bad records are in the DLQ with their key, value and headers.
	dead := readAll(t, in.brokers, dlqTopic, 2)
	if len(dead) != 2 {
		t.Fatalf("dlq has %d records, want 2", len(dead))
	}
	byKey := map[string]*kgo.Record{}
	for _, r := range dead {
		byKey[string(r.Key)] = r
	}
	for key, c := range map[string]struct{ reason, value string }{
		"poison":  {kafka.ReasonHandlerFailed, listing("poison")},
		"garbage": {kafka.ReasonUndecodable, "this is not json"},
	} {
		r := byKey[key]
		if r == nil {
			t.Fatalf("no dead letter for %s", key)
		}
		want := map[string]string{
			"trace":                   "t-" + key,
			kafka.HeaderOriginalTopic: topic,

			kafka.HeaderGroup:  group,
			kafka.HeaderReason: c.reason,
		}
		for h, v := range want {
			if got := headerOf(r, h); got != v {
				t.Errorf("%s: header %s = %q, want %q", key, h, got, v)
			}
		}
		if headerOf(r, kafka.HeaderOffset) == "" || headerOf(r, kafka.HeaderPartition) == "" || headerOf(r, kafka.HeaderError) == "" {
			t.Errorf("%s: missing offset or error header", key)
		}
		if _, err := time.Parse(time.RFC3339Nano, headerOf(r, kafka.HeaderFailedAt)); err != nil {
			t.Errorf("%s: failed_at: %v", key, err)
		}
		if string(r.Value) != c.value {
			t.Errorf("%s: value %q, want %q", key, r.Value, c.value)
		}
	}

	// Restarting the consumer must not redeliver them: they were committed.
	stop()
	before := attemptsOf(&mu, &attempts)
	stopAgain := run(t, "consumer again", func(ctx context.Context) error { return kafka.Consume(ctx, sub, handle) })
	consistently(t, 3*time.Second, "no redelivery after restart", func() bool { return attemptsOf(&mu, &attempts) == before && len(snapshot()) == 2 })
	if before != 5 {
		t.Errorf("handler attempts for the poison record = %d, want 5", before)
	}

	// Listing shows both, without payloads.
	replayGroup := DLQReplayGroup(common, events.TopicListingsNew)
	items, total, err := kafka.ListDeadLetters(ctx, in.brokers, topic, replayGroup, 10)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("list = %+v, %d, %v", items, total, err)
	}
	if items[0].Reason == "" || items[0].Key == "" || items[0].Error == "" || items[0].Group != group {
		t.Errorf("list item = %+v", items[0])
	}
	if _, total, _ := kafka.ListDeadLetters(ctx, in.brokers, topic, replayGroup, 1); total != 2 {
		t.Errorf("limited list total = %d, want 2", total)
	}

	// Replay puts them back on the original topic, once, without dlq.* headers.
	stopAgain() // a running consumer would just dead-letter them again
	n, err := kafka.ReplayDeadLetters(ctx, in.brokers, topic, replayGroup)
	if err != nil || n != 2 {
		t.Fatalf("replay = %d, %v", n, err)
	}
	all := readAll(t, in.brokers, topic, 6)
	if len(all) != 6 {
		t.Fatalf("topic has %d records after replay, want 6", len(all))
	}
	counts := map[string]int{}
	for _, r := range all {
		key := string(r.Key)
		counts[key]++
		if headerOf(r, "trace") != "t-"+key {
			t.Errorf("record %s lost its headers: %v", key, r.Headers)
		}
		for _, h := range r.Headers {
			if strings.HasPrefix(h.Key, "dlq.") {
				t.Errorf("record %s has header %s on the original topic", key, h.Key)
			}
		}
	}
	if want := map[string]int{"good0": 1, "good1": 1, "poison": 2, "garbage": 2}; !maps.Equal(counts, want) {
		t.Errorf("keys on the topic after replay = %v, want %v", counts, want)
	}
	if n, err := kafka.ReplayDeadLetters(ctx, in.brokers, topic, replayGroup); err != nil || n != 0 {
		t.Errorf("second replay = %d, %v; want 0", n, err)
	}
	if _, total, _ := kafka.ListDeadLetters(ctx, in.brokers, topic, replayGroup, 10); total != 0 {
		t.Errorf("list after replay total = %d, want 0", total)
	}
	if got := readAll(t, in.brokers, topic, 6); len(got) != 6 {
		t.Errorf("topic has %d records after second replay, want 6", len(got))
	}
}

func attemptsOf(mu *sync.Mutex, n *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *n
}
