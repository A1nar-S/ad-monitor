package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestTransient(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("bad filters"), false},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"connection failure", fmt.Errorf("upsert: %w", &pgconn.PgError{Code: "08006"}), true},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, true},
		{"not migrated yet", &pgconn.PgError{Code: "42P01"}, true},
		{"unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"connect", fmt.Errorf("acquire: %w", &pgconn.ConnectError{}), true},
		// An HTTP call timing out or failing is the handler's own trouble.
		{"other timeout", fmt.Errorf("discord: %w", context.DeadlineExceeded), false},
		{"other network", &net.OpError{Op: "dial", Err: errors.New("refused")}, false},
	} {
		if got := Transient(tc.err); got != tc.want {
			t.Errorf("%s: Transient = %v, want %v", tc.name, got, tc.want)
		}
	}
}
