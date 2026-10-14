// Package releasetrain is the pure core of the Release Train monitor: it joins observed facts into a Board.
package releasetrain

import (
	"encoding/json"
	"fmt"
	"time"
)

// Day is a civil date in the configured timezone.
type Day struct {
	Y int
	M time.Month
	D int
}

const dayLayout = "2006-01-02"

// DayOf returns the civil date of t in loc.
func DayOf(t time.Time, loc *time.Location) Day {
	y, m, d := t.In(loc).Date()
	return Day{y, m, d}
}

// ParseDay parses "YYYY-MM-DD".
func ParseDay(s string) (Day, error) {
	t, err := time.Parse(dayLayout, s)
	if err != nil {
		return Day{}, fmt.Errorf("parse day %q: %w", s, err)
	}
	return DayOf(t, time.UTC), nil
}

func (d Day) utc() time.Time { return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, time.UTC) }

// Add returns the day n days after d.
func (d Day) Add(n int) Day { return DayOf(d.utc().AddDate(0, 0, n), time.UTC) }

// Before reports whether d is earlier than o.
func (d Day) Before(o Day) bool { return d.utc().Before(o.utc()) }

// After reports whether d is later than o.
func (d Day) After(o Day) bool { return d.utc().After(o.utc()) }

// Sub returns the number of days from o to d.
func (d Day) Sub(o Day) int { return int(d.utc().Sub(o.utc()).Hours() / 24) }

// String formats d as "YYYY-MM-DD".
func (d Day) String() string { return d.utc().Format(dayLayout) }

func (d Day) label() string { return d.utc().Format("Mon 2 Jan") }

// MarshalJSON writes d as "YYYY-MM-DD".
func (d Day) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON reads "YYYY-MM-DD".
func (d *Day) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("day: %w", err)
	}
	parsed, err := ParseDay(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func minDay(a, b *Day) *Day {
	if a == nil || (b != nil && b.Before(*a)) {
		return b
	}
	return a
}

func maxDay(a, b *Day) *Day {
	if a == nil || (b != nil && b.After(*a)) {
		return b
	}
	return a
}
