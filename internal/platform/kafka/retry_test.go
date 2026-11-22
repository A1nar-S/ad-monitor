package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

var errDown = errors.New("database down")

func TestRetryGivesUpOnOrdinaryErrors(t *testing.T) {
	calls := 0
	err := retry(context.Background(), Subscription{RetryDelay: time.Microsecond}, func() error {
		calls++
		return errors.New("bad")
	})
	if err == nil || calls != maxAttempts {
		t.Fatalf("err = %v after %d calls, want an error after %d", err, calls, maxAttempts)
	}
}

func TestRetryStopsAtOnceOnPermanent(t *testing.T) {
	calls := 0
	err := retry(context.Background(), Subscription{RetryDelay: time.Microsecond}, func() error {
		calls++
		return Permanent(errors.New("undecodable filters"))
	})
	if err == nil || calls != 1 {
		t.Fatalf("err = %v after %d calls, want an error after 1", err, calls)
	}
}

func TestRetryKeepsGoingOnTransient(t *testing.T) {
	sub := Subscription{RetryDelay: time.Microsecond, Transient: func(err error) bool { return errors.Is(err, errDown) }}
	calls := 0
	err := retry(context.Background(), sub, func() error {
		if calls++; calls < 3*maxAttempts {
			return errDown
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want success after %d calls", err, calls)
	}
}

func TestRetryTreatsFailedPublishAsTransient(t *testing.T) {
	calls := 0
	err := retry(context.Background(), Subscription{RetryDelay: time.Microsecond}, func() error {
		if calls++; calls < 2*maxAttempts {
			return &publishError{topic: "t", err: errors.New("broker down")}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want success after %d calls", err, calls)
	}
}

func TestPublishFailed(t *testing.T) {
	var pe *publishError
	if err := publishFailed("t", kgo.ErrRecordTimeout); !errors.As(err, &pe) {
		t.Errorf("timed out: %v, want retried until it works", err)
	}
	var perm permanentError
	if err := publishFailed("t", kerr.MessageTooLarge); !errors.As(err, &perm) {
		t.Errorf("too large: %v, want permanent", err)
	}
	if err := publishFailed("t", kerr.TopicAuthorizationFailed); errors.As(err, &pe) || errors.As(err, &perm) {
		t.Errorf("not authorized: %v, want an ordinary error", err)
	}
}

func TestRetryTransientStopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := Subscription{RetryDelay: time.Microsecond, Transient: func(error) bool { return true }}
	calls := 0
	err := retry(ctx, sub, func() error {
		if calls++; calls == 3 {
			cancel()
		}
		return errDown
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
