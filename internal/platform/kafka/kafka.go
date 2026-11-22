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

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	maxAttempts = 5
	retryDelay  = 2 * time.Second
	// maxRetryDelay caps the wait between attempts of a transient failure,
	// which is retried until it works.
	maxRetryDelay = time.Minute
	// deliveryTimeout is how long a publish keeps trying, through a broker
	// restart, before it fails.
	deliveryTimeout = 30 * time.Second
)

// Permanent marks a handler error retrying cannot fix (bad input): the
// record is dead-lettered at once.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// publishError is a publish that failed because the broker was down or
// slow, not because of the record, so a handler failing with it is retried
// until it works.
type publishError struct {
	topic string
	err   error
}

func (e *publishError) Error() string { return "publish to " + e.topic + ": " + e.err.Error() }
func (e *publishError) Unwrap() error { return e.err }

// Producer publishes JSON values.
type Producer struct {
	cl *kgo.Client
}

func NewProducer(brokers []string) (*Producer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.AllowAutoTopicCreation(),
		kgo.RecordPartitioner(kgo.UniformBytesPartitioner(64<<10, true, true, nil)),
		kgo.RecordDeliveryTimeout(deliveryTimeout),
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
		return Permanent(fmt.Errorf("marshal for %s: %w", topic, err))
	}
	rec := &kgo.Record{Topic: topic, Key: []byte(key), Value: b}
	if err := p.cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		published.WithLabelValues(topic, "error").Inc()
		return publishFailed(topic, err)
	}
	published.WithLabelValues(topic, "ok").Inc()
	return nil
}

func (p *Producer) Close() { p.cl.Close() }

// publishFailed classifies a failed publish: the broker's (down, slow) is a
// publishError, retried until it works; one the record itself caused (too
// large) is Permanent; anything else an ordinary error.
func publishFailed(topic string, err error) error {
	switch {
	case errors.Is(err, kgo.ErrRecordTimeout), errors.Is(err, kgo.ErrRecordRetries), kerr.IsRetriable(err):
		return &publishError{topic: topic, err: err}
	case errors.Is(err, kerr.MessageTooLarge):
		return Permanent(fmt.Errorf("publish to %s: %w", topic, err))
	}
	return fmt.Errorf("publish to %s: %w", topic, err)
}

// Subscription says what Consume reads: Topic as part of Group.
type Subscription struct {
	Brokers []string
	Group   string
	Topic   string
	// RetryDelay is the base wait between handler attempts; 0 = 2s. Tests shorten it.
	RetryDelay time.Duration
	// Transient reports a handler error caused by something outside the
	// record (the database is down): such a record is retried until it
	// works instead of being dead-lettered. Failed publishes always count.
	Transient func(error) bool
}

// transient reports whether err is retried until it works.
func (s Subscription) transient(err error) bool {
	var pe *publishError
	return errors.As(err, &pe) || s.Transient != nil && s.Transient(err)
}

// Consume reads sub.Topic as part of sub.Group and calls handle for every
// record, committing the offset only after handle succeeds (at-least-once). A
// failing handler is retried a few times; then the record, like an
// undecodable one, is published to the dead-letter topic DLQTopic(sub.Topic)
// so one poison message cannot block the partition and nothing is lost. A
// Permanent error is dead-lettered at once; a transient one (sub.Transient,
// a failed publish) is retried until it works, so an outage of the database
// or the broker delays records instead of dead-lettering them all. If
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
			} else if err := handleObserved(ctx, topic, sub, v, handle); err != nil {
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

// retry calls fn until it succeeds: up to maxAttempts times, waiting
// longer each time, for an ordinary error; once for a Permanent one; and for
// as long as it takes for a transient one.
func retry(ctx context.Context, sub Subscription, fn func() error) error {
	delay := sub.RetryDelay
	if delay <= 0 {
		delay = retryDelay
	}
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		var pe permanentError
		transient := sub.transient(err)
		if errors.As(err, &pe) || !transient && attempt >= maxAttempts {
			return err
		}
		wait := min(delay*time.Duration(attempt), max(maxRetryDelay, delay))
		slog.Warn("handler error, retrying", "attempt", attempt, "transient", transient, "in", wait, "err", err)
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
