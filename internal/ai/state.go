package ai

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// DB is what the state needs of the database.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const limitKey = "claude_limit_until"

// LimitUntil is when Claude Code's usage limit resets; zero when it isn't
// reached (or has reset by now).
func LimitUntil(ctx context.Context, db DB, now time.Time) time.Time {
	var v string
	err := db.QueryRowContext(ctx, "SELECT value FROM ai_state WHERE key = ?", limitKey).Scan(&v)
	if err != nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	if t := time.Unix(sec, 0); t.After(now) {
		return t
	}
	return time.Time{}
}

// SetLimit records that the limit is reached until until.
func SetLimit(ctx context.Context, db DB, until time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO ai_state (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, limitKey, strconv.FormatInt(until.Unix(), 10))
	return err
}

// ResetAt is when to try again after err, a *LimitError: its reset time
// plus a minute, or an hour from now when it is unknown.
func ResetAt(err error, now time.Time) time.Time {
	var le *LimitError
	if errors.As(err, &le) && le.Until.After(now) {
		return le.Until.Add(time.Minute)
	}
	return now.Add(time.Hour)
}
