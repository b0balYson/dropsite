package main

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/client"
)

const dateOnly = "2006-01-02"

var errExpires = errors.New("--expires takes a duration in days or hours (7d, 36h), " +
	"a date (2026-12-31, the site works through that day, UTC), an RFC 3339 time or never")

// parseExpires turns the --expires value into an expiry relative to now.
func parseExpires(v string, now time.Time) (*client.Expiry, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "never" || v == "none" {
		return &client.Expiry{}, nil
	}
	if n := len(v); n > 1 && (v[n-1] == 'd' || v[n-1] == 'h') {
		count, err := strconv.Atoi(v[:n-1])
		if err != nil || count <= 0 || count > 10*366*24 {
			return nil, errExpires
		}
		unit := time.Hour
		if v[n-1] == 'd' {
			unit = 24 * time.Hour
		}
		at := now.Add(time.Duration(count) * unit).UTC().Truncate(time.Second)
		return &client.Expiry{At: &at}, nil
	}
	if d, err := time.Parse(dateOnly, v); err == nil {
		at := d.Add(24 * time.Hour)
		return &client.Expiry{At: &at}, nil
	}
	if t, err := time.Parse(time.RFC3339, strings.ToUpper(v)); err == nil {
		at := t.UTC()
		return &client.Expiry{At: &at}, nil
	}
	return nil, errExpires
}
