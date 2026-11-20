package kafka

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Dead-letter records keep the original key, value and headers, plus these
// headers describing why and where the record failed.
const (
	HeaderOriginalTopic = "dlq.original_topic"
	HeaderPartition     = "dlq.partition"
	HeaderOffset        = "dlq.offset"
	HeaderGroup         = "dlq.group"
	HeaderError         = "dlq.error"
	HeaderFailedAt      = "dlq.failed_at"
	HeaderReason        = "dlq.reason"

	headerPrefix = "dlq."
)

// Reasons a record is dead-lettered.
const (
	ReasonHandlerFailed = "handler_failed"
	ReasonUndecodable   = "undecodable"
)

const (
	// maxErrorLen bounds the error text in a header, in bytes.
	maxErrorLen = 1024
	// maxDLQBackoff caps the wait between attempts to publish a dead letter.
	maxDLQBackoff = 30 * time.Second
)

// DLQTopic is the dead-letter topic of topic. topic is already namespaced
// (config.Common.Topic), so the result is too.
func DLQTopic(topic string) string { return topic + ".dlq" }

// truncate cuts s to at most max bytes without splitting a UTF-8 character,
// marking the cut with an ellipsis.
func truncate(s string, max int) string {
	const ell = "…"
	if len(s) <= max {
		return s
	}
	cut := max - len(ell)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ell
}

// deadLetter builds the record to publish to the dead-letter topic of rec.
func deadLetter(rec *kgo.Record, group, reason string, cause error, at time.Time) *kgo.Record {
	headers := make([]kgo.RecordHeader, 0, len(rec.Headers)+7)
	for _, h := range rec.Headers {
		if !strings.HasPrefix(h.Key, headerPrefix) { // never inherit a stale dlq.* from an earlier round
			headers = append(headers, h)
		}
	}
	msg := ""
	if cause != nil {
		msg = truncate(cause.Error(), maxErrorLen)
	}
	add := func(k, v string) { headers = append(headers, kgo.RecordHeader{Key: k, Value: []byte(v)}) }
	add(HeaderOriginalTopic, rec.Topic)
	add(HeaderPartition, strconv.Itoa(int(rec.Partition)))
	add(HeaderOffset, strconv.FormatInt(rec.Offset, 10))
	add(HeaderGroup, group)
	add(HeaderError, msg)
	add(HeaderFailedAt, at.UTC().Format(time.RFC3339Nano))
	add(HeaderReason, reason)
	return &kgo.Record{Topic: DLQTopic(rec.Topic), Key: rec.Key, Value: rec.Value, Headers: headers}
}

// header returns the value of the named header, or "".
func header(rec *kgo.Record, key string) string {
	for _, h := range rec.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// publishDeadLetter publishes rec until it succeeds. Only a cancelled ctx
// stops it, and then the caller must leave the source record uncommitted so
// it is redelivered: a record is never dropped.
func publishDeadLetter(ctx context.Context, cl *kgo.Client, rec *kgo.Record) error {
	delay := retryDelay
	for {
		err := cl.ProduceSync(ctx, rec).FirstErr()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Error("dead-letter publish failed, retrying", "topic", rec.Topic, "err", err)
		if !sleep(ctx, delay) {
			return ctx.Err()
		}
		delay = min(delay*2, maxDLQBackoff)
	}
}
