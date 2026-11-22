package db

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Transient reports whether err comes from the database being unreachable,
// restarting or briefly overloaded, or from a table its owner has not
// migrated yet: trying again later can succeed. Consumers retry such
// records until they do (kafka.Subscription.Transient). Only pgx's own
// errors count: a timeout or network error of an HTTP call is not an outage
// of the database.
func Transient(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch c := pgErr.Code; {
		case strings.HasPrefix(c, "08"), // connection exception
			strings.HasPrefix(c, "53"),  // insufficient resources
			strings.HasPrefix(c, "57P"), // shutting down, cannot connect now
			c == "40001", c == "40P01",  // serialization failure, deadlock
			c == "42P01": // undefined table: its owner migrates it soon
			return true
		}
		return false
	}
	var connErr *pgconn.ConnectError
	return errors.As(err, &connErr) || pgconn.Timeout(err) || pgconn.SafeToRetry(err)
}
