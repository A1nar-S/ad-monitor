package db

import (
	"testing"
	"testing/fstest"
)

func TestLatestVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_listings.sql":     {},
		"0010_seen_by_feed.sql": {},
		"0002_outbox.sql":       {},
		"README.md":             {},
	}
	if v, err := latestVersion(fsys); err != nil || v != 10 {
		t.Fatalf("latestVersion = %d, %v; want 10", v, err)
	}
	if _, err := latestVersion(fstest.MapFS{}); err == nil {
		t.Fatal("no migrations: want an error")
	}
}
