package bot

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

func TestCommandLabelIsBounded(t *testing.T) {
	for in, want := range map[string]string{
		CmdWatch: "watch", ActionToggle: "menu_toggle", "x' OR 1=1": "other", "": "other",
	} {
		if got := commandLabel(in); got != want {
			t.Errorf("commandLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObserveCommandResults(t *testing.T) {
	rejected := commandsTotal.WithLabelValues("unwatch", "rejected")
	failed := commandsTotal.WithLabelValues("unwatch", "error")
	r0, f0 := testutil.ToFloat64(rejected), testutil.ToFloat64(failed)

	observeCommand(CmdUnwatch, subscriptions.ErrNotFound)
	observeCommand(CmdUnwatch, errors.New("db down"))

	if d := testutil.ToFloat64(rejected) - r0; d != 1 {
		t.Errorf("rejected rose by %v, want 1", d)
	}
	if d := testutil.ToFloat64(failed) - f0; d != 1 {
		t.Errorf("error rose by %v, want 1", d)
	}
}
