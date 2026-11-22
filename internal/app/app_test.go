package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A chat platform failing is served again, and never returns an error that
// would stop the dashboard beside it.
func TestKeepServingRetries(t *testing.T) {
	chatRetryFirst, chatRetryMax = time.Millisecond, 2*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	serve := keepServing("test", func(ctx context.Context) error {
		if calls++; calls < 3 {
			return errors.New("gateway down")
		}
		cancel()
		<-ctx.Done()
		return nil
	})
	if err := serve(ctx); err != nil || calls != 3 {
		t.Fatalf("err = %v after %d calls, want nil after 3", err, calls)
	}
}
