// Package kafka wraps franz-go with the small surface the services need:
// publish a JSON value, and consume a topic with at-least-once handling.
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	maxAttempts = 5
	retryDelay  = 2 * time.Second
)

// Producer publishes JSON values.
type Producer struct {
	cl *kgo.Client
}

func NewProducer(brokers []string) (*Producer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.AllowAutoTopicCreation(),
		kgo.RecordPartitioner(kgo.UniformBytesPartitioner(64<<10, true, true, nil)),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{cl: cl}, nil
}

// Publish marshals v and waits for the broker to acknowledge it. Records with
// the same key land on the same partition and keep their order.
func (p *Producer) Publish(ctx context.Context, topic, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		published.WithLabelValues(topic, "error").Inc()
		return fmt.Errorf("marshal for %s: %w", topic, err)
	}
	rec := &kgo.Record{Topic: topic, Key: []byte(key), Value: b}
	if err := p.cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		published.WithLabelValues(topic, "error").Inc()
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	published.WithLabelValues(topic, "ok").Inc()
	return nil
}

func (p *Producer) Close() { p.cl.Close() }

// Subscription says what Consume reads: Topic as part of Group.
type Subscription struct {
	Brokers []string
	Group   string
	Topic   string
	// RetryDelay is the base wait between handler attempts; 0 = 2s. Tests shorten it.
	RetryDelay time.Duration
}

// Consume reads sub.Topic as part of sub.Group and calls handle for every
// record, committing the offset only after handle succeeds (at-least-once). A
// failing handler is retried a few times; then the record, like an
// undecodable one, is published to the dead-letter topic DLQTopic(sub.Topic)
// so one poison message cannot block the partition and nothing is lost. If
// that publish fails it is retried until it works; on shutdown the record is
// left uncommitted and delivered again later. Handlers must be idempotent.
// Returns when ctx is done.
func Consume[T any](ctx context.Context, sub Subscription, handle func(context.Context, T) error) error {
	topic, group := sub.Topic, sub.Group
	initConsumeSeries(topic)
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(sub.Brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.AllowAutoTopicCreation(),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return fmt.Errorf("kafka consumer: %w", err)
	}
	defer cl.Close()

	for {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				slog.Error("kafka fetch", "topic", e.Topic, "partition", e.Partition, "err", e.Err)
			}
			if errors.Is(fetches.Err0(), kgo.ErrClientClosed) {
				return fetches.Err0()
			}
			sleep(ctx, retryDelay)
			continue
		}

		var done bool
		fetches.EachRecord(func(rec *kgo.Record) {
			if done {
				return
			}
			// Every record ends in one of two outcomes: handled, or (reason,
			// cause) set and dead-lettered below.
			var v T
			var reason string
			var cause error
			if err := json.Unmarshal(rec.Value, &v); err != nil {
				reason, cause = ReasonUndecodable, err
			} else if err := handleObserved(ctx, topic, sub.RetryDelay, v, handle); err != nil {
				if ctx.Err() != nil {
					done = true // shutting down: leave the record uncommitted
					return
				}
				reason, cause = ReasonHandlerFailed, err
			}
			if cause != nil {
				slog.Error("dead-lettering record", "topic", topic, "partition", rec.Partition, "offset", rec.Offset, "reason", reason, "err", cause)
				if err := publishDeadLetter(ctx, cl, deadLetter(rec, group, reason, cause, time.Now())); err != nil {
					done = true // shutting down: leave the record uncommitted
					return
				}
				consumed.WithLabelValues(topic, "dead_lettered").Inc()
				deadLettered.WithLabelValues(topic, reason).Inc()
			} else {
				consumed.WithLabelValues(topic, "ok").Inc()
			}
			if err := cl.CommitRecords(ctx, rec); err != nil && ctx.Err() == nil {
				slog.Error("commit failed", "topic", topic, "offset", rec.Offset, "err", err)
			}
		})
		if done {
			return nil
		}
	}
}

func retry(ctx context.Context, delay time.Duration, fn func() error) error {
	if delay <= 0 {
		delay = retryDelay
	}
	var err error
	for i := 0; i < maxAttempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		slog.Warn("handler error, retrying", "attempt", i+1, "err", err)
		if i == maxAttempts-1 {
			break // out of attempts; no point waiting
		}
		if !sleep(ctx, delay*time.Duration(i+1)) {
			return ctx.Err()
		}
	}
	return err
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
