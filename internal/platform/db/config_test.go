package db

import "testing"

func TestConfigJIT(t *testing.T) {
	for url, want := range map[string]string{
		"postgres://u:p@localhost:5432/d":        "off",
		"postgres://u:p@localhost:5432/d?jit=on": "on",
	} {
		cfg, err := config(url)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.ConnConfig.RuntimeParams["jit"]; got != want {
			t.Errorf("%s: jit = %q, want %q", url, got, want)
		}
	}
}
