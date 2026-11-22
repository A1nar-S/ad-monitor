// Package db holds database plumbing shared by slices.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Open connects to Postgres and checks the connection. JIT is off unless
// the URL turns it on: every query here is short, and compiling one (on
// whenever Postgres overestimates a query's cost, as with the gem finder's
// facet counts over JSON attrs) can take ten times as long as running it.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := config(url)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return pool, nil
}

// config parses url, with JIT off unless url sets it (?jit=on).
func config(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if _, set := cfg.ConnConfig.RuntimeParams["jit"]; !set {
		cfg.ConnConfig.RuntimeParams["jit"] = "off"
	}
	return cfg, nil
}

// Migrate applies the pending goose migrations in fsys. Every slice owns its
// migrations and tracks them in its own version table ("<scope>_migrations"),
// so slices never share a migration sequence. A Postgres advisory lock lets
// several services migrate the same slice when they start together.
func Migrate(ctx context.Context, pool *pgxpool.Pool, scope string, fsys fs.FS) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migrations %s: %w", scope, err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithTableName(scope+"_migrations"), goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migrations %s: %w", scope, err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrations %s: %w", scope, err)
	}
	return nil
}

// awaitEvery is how often AwaitMigrated looks again.
const awaitEvery = 2 * time.Second

// AwaitMigrated blocks until the schema of scope is at least at the newest
// migration in fsys. A service that only reads another slice's tables calls
// it instead of Migrate: the slice's owner migrates them, and this build
// waits for the schema it was written against.
func AwaitMigrated(ctx context.Context, pool *pgxpool.Pool, scope string, fsys fs.FS) error {
	want, err := latestVersion(fsys)
	if err != nil {
		return fmt.Errorf("migrations %s: %w", scope, err)
	}
	logged := false
	for {
		have, err := appliedVersion(ctx, pool, scope)
		switch {
		case err == nil && have >= want:
			return nil
		case err != nil && !Transient(err):
			return fmt.Errorf("schema %s: %w", scope, err)
		case !logged:
			slog.Info("waiting for another service to migrate", "scope", scope, "have", have, "want", want)
			logged = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(awaitEvery):
		}
	}
}

// appliedVersion is the newest migration of scope applied; 0 before any.
func appliedVersion(ctx context.Context, pool *pgxpool.Pool, scope string) (int64, error) {
	var v int64
	// scope is one of the slices' fixed names, never input.
	err := pool.QueryRow(ctx, "SELECT COALESCE(max(version_id), 0) FROM "+scope+"_migrations WHERE is_applied").Scan(&v)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		return 0, nil // no migration yet
	}
	return v, err
}

// latestVersion is the number of the newest migration file in fsys
// ("0007_seen_by_feed.sql" is 7).
func latestVersion(fsys fs.FS) (int64, error) {
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, f := range files {
		num, _, ok := strings.Cut(path.Base(f), "_")
		if !ok {
			continue
		}
		v, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("migration %s: %w", f, err)
		}
		latest = max(latest, v)
	}
	if latest == 0 {
		return 0, errors.New("no migrations")
	}
	return latest, nil
}
