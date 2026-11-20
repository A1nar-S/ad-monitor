package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// How long the admin tools wait for records that should be there before giving up.
const idleTimeout = 30 * time.Second

// maxKeyLen bounds the key shown by ListDeadLetters, in bytes.
const maxKeyLen = 80

// DeadLetter summarises one dead-lettered record, without its payload.
type DeadLetter struct {
	Partition int32 // of the dead-letter topic
	Offset    int64
	Reason    string
	Error     string
	FailedAt  string
	Key       string
	Group     string // consumer group that gave up on it
	Size      int    // payload bytes
}

// ListDeadLetters returns up to limit dead letters of topic (the original,
// namespaced topic) that have not been replayed yet, i.e. are past the
// offsets group committed, and how many such letters exist in all. It
// commits nothing.
func ListDeadLetters(ctx context.Context, brokers []string, topic, group string, limit int) ([]DeadLetter, int64, error) {
	dlq := DLQTopic(topic)
	ranges, err := pendingRanges(ctx, brokers, dlq, group)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	for _, r := range ranges {
		total += r.end - r.from
	}
	if total == 0 {
		return nil, 0, nil
	}
	offsets := map[int32]kgo.Offset{}
	for p, r := range ranges {
		offsets[p] = kgo.NewOffset().At(r.from)
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{dlq: offsets}))
	if err != nil {
		return nil, 0, fmt.Errorf("kafka consumer: %w", err)
	}
	defer cl.Close()

	var out []DeadLetter
	err = readRanges(ctx, cl, ranges, func(rec *kgo.Record) error {
		out = append(out, DeadLetter{
			Partition: rec.Partition,
			Offset:    rec.Offset,
			Reason:    header(rec, HeaderReason),
			Error:     header(rec, HeaderError),
			FailedAt:  header(rec, HeaderFailedAt),
			Key:       truncate(string(rec.Key), maxKeyLen),
			Group:     header(rec, HeaderGroup),
			Size:      len(rec.Value),
		})
		if len(out) >= limit {
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, 0, err
	}
	return out, total, nil
}

// ReplayDeadLetters publishes every pending dead letter of topic back to topic
// and commits it as group, so a second run replays nothing again. It returns
// how many records it replayed. A record is committed only after the broker
// acknowledged its republishing, so a crash can replay a record twice but
// never lose one; consumers are idempotent.
func ReplayDeadLetters(ctx context.Context, brokers []string, topic, group string) (int, error) {
	dlq := DLQTopic(topic)
	ranges, err := pendingRanges(ctx, brokers, dlq, group)
	if err != nil {
		return 0, err
	}
	if len(ranges) == 0 {
		return 0, nil
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(dlq),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return 0, fmt.Errorf("kafka consumer: %w", err)
	}
	defer cl.Close()

	n := 0
	err = readRanges(ctx, cl, ranges, func(rec *kgo.Record) error {
		var headers []kgo.RecordHeader
		for _, h := range rec.Headers {
			if !strings.HasPrefix(h.Key, headerPrefix) {
				headers = append(headers, h)
			}
		}
		orig := &kgo.Record{Topic: topic, Key: rec.Key, Value: rec.Value, Headers: headers}
		if err := cl.ProduceSync(ctx, orig).FirstErr(); err != nil {
			return fmt.Errorf("republish to %s: %w", topic, err)
		}
		if err := cl.CommitRecords(ctx, rec); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		n++
		return nil
	})
	return n, err
}

// offsetRange is the part of one partition still to read: [from, end).
type offsetRange struct{ from, end int64 }

// pendingRanges finds, per partition of dlq, the records between what group
// committed (or the start of the log) and the end of the log as it is now.
// Partitions with nothing pending, and a missing topic, give no entry.
func pendingRanges(ctx context.Context, brokers []string, dlq, group string) (map[int32]offsetRange, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, fmt.Errorf("kafka client: %w", err)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)

	starts, err := adm.ListStartOffsets(ctx, dlq)
	if err == nil {
		err = starts.Error()
	}
	if errors.Is(err, kerr.UnknownTopicOrPartition) {
		return nil, nil // never created: nothing was dead-lettered
	}
	if err != nil {
		return nil, fmt.Errorf("list start offsets of %s: %w", dlq, err)
	}
	ends, err := adm.ListEndOffsets(ctx, dlq)
	if err == nil {
		err = ends.Error()
	}
	if err != nil {
		return nil, fmt.Errorf("list end offsets of %s: %w", dlq, err)
	}
	committed, err := adm.FetchOffsets(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("fetch offsets of group %s: %w", group, err)
	}

	out := map[int32]offsetRange{}
	ends.Each(func(end kadm.ListedOffset) {
		from := int64(0)
		if s, ok := starts.Lookup(dlq, end.Partition); ok {
			from = s.Offset
		}
		if c, ok := committed.Lookup(dlq, end.Partition); ok && c.At > from {
			from = c.At
		}
		if from < end.Offset {
			out[end.Partition] = offsetRange{from: from, end: end.Offset}
		}
	})
	return out, nil
}

// errStop ends readRanges early without it being an error.
var errStop = errors.New("stop")

// readRanges feeds each record of the ranges to fn, in order per partition,
// and returns once all are read, fn fails, or no record arrives for a while.
func readRanges(ctx context.Context, cl *kgo.Client, ranges map[int32]offsetRange, fn func(*kgo.Record) error) error {
	left := len(ranges)
	for left > 0 {
		pctx, cancel := context.WithTimeout(ctx, idleTimeout)
		fetches := cl.PollFetches(pctx)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, e := range fetches.Errors() {
			if errors.Is(e.Err, context.DeadlineExceeded) {
				return fmt.Errorf("no records for %s, %d partitions unread", idleTimeout, left)
			}
			return fmt.Errorf("fetch %s/%d: %w", e.Topic, e.Partition, e.Err)
		}
		var err error
		fetches.EachRecord(func(rec *kgo.Record) {
			r, ok := ranges[rec.Partition]
			if err != nil || !ok || rec.Offset >= r.end {
				return
			}
			if err = fn(rec); err != nil {
				return
			}
			if rec.Offset+1 >= r.end {
				left--
				delete(ranges, rec.Partition)
			}
		})
		if err != nil {
			return err
		}
		// The last offset below end may never be seen (deleted by retention,
		// or a control marker): a partition is also done once the client's
		// position has passed end.
		for _, parts := range cl.UncommittedOffsets() {
			for p, o := range parts {
				if _, ok := ranges[p]; ok && o.Offset >= ranges[p].end {
					left--
					delete(ranges, p)
				}
			}
		}
	}
	return nil
}
