// Package db holds database plumbing shared by slices.
package db

import (
	"context"
	"fmt"
	"io/fs"

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
