package releasetrain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDayOfUsesTheLocationsCalendarDate(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*3600)
	got := DayOf(time.Date(2026, 10, 13, 18, 30, 0, 0, time.UTC), loc)
	if want := (Day{2026, time.October, 14}); got != want {
		t.Fatalf("DayOf = %v, want %v", got, want)
	}
}

func TestDayArithmetic(t *testing.T) {
	d := day(t, "2026-12-30")
	if got := d.Add(3).String(); got != "2027-01-02" {
		t.Fatalf("Add(3) = %s, want 2027-01-02", got)
	}
	if got := d.Add(-30).String(); got != "2026-11-30" {
		t.Fatalf("Add(-30) = %s, want 2026-11-30", got)
	}
	if got := day(t, "2027-01-02").Sub(d); got != 3 {
		t.Fatalf("Sub = %d, want 3", got)
	}
	if !d.Before(d.Add(1)) || d.Before(d) || !d.Add(1).After(d) || d.After(d) {
		t.Fatalf("Before/After wrong around %s", d)
	}
}

func TestDayAcrossDaylightSavingIsWholeDays(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	a := DayOf(time.Date(2026, 3, 28, 12, 0, 0, 0, loc), loc)
	b := DayOf(time.Date(2026, 3, 30, 12, 0, 0, 0, loc), loc)
	if got := b.Sub(a); got != 2 {
		t.Fatalf("Sub across DST = %d, want 2", got)
	}
}

func TestParseDayRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "2026-13-01", "14/10/2026", "2026-10-14T00:00:00Z"} {
		if _, err := ParseDay(s); err == nil {
			t.Fatalf("ParseDay(%q) = nil error, want error", s)
		}
	}
}

func TestDayJSONRoundTrip(t *testing.T) {
	in := struct {
		D Day `json:"d"`
	}{day(t, "2026-10-14")}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"d":"2026-10-14"}` {
		t.Fatalf("marshal = %s", got)
	}
	var out struct {
		D Day `json:"d"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.D != in.D {
		t.Fatalf("round trip = %v, want %v", out.D, in.D)
	}
	if err := json.Unmarshal([]byte(`{"d":"soon"}`), &out); err == nil {
		t.Fatalf("unmarshal of a bad day succeeded")
	}
}
