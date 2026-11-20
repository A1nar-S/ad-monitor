package config

import (
	"os"
	"testing"
)

func TestMetricsAddrDefaultsToLoopback(t *testing.T) {
	t.Setenv("METRICS_ADDR", "x") // registers the restore
	_ = os.Unsetenv("METRICS_ADDR")
	if got := metricsAddr(); got != "127.0.0.1:9100" {
		t.Errorf("default = %q", got)
	}
}

func TestMetricsAddr(t *testing.T) {
	t.Setenv("METRICS_ADDR", "")
	if got := metricsAddr(); got != "" {
		t.Errorf("empty METRICS_ADDR = %q, want disabled", got)
	}
	t.Setenv("METRICS_ADDR", "0.0.0.0:9200")
	if got := metricsAddr(); got != "0.0.0.0:9200" {
		t.Errorf("METRICS_ADDR = %q", got)
	}
}
