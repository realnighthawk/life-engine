package engine

import (
	"testing"
	"time"
)

func at(t *testing.T, zoneName, local string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zoneName)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02 15:04", local, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

func TestNextOccurrence(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	cases := []struct {
		name       string
		due, after time.Time
		rec        Recurrence
		want       time.Time
	}{
		{"daily", at(t, "Europe/London", "2026-10-12 09:00"), at(t, "Europe/London", "2026-10-12 09:00"), Recurrence{Freq: "daily", Interval: 1}, at(t, "Europe/London", "2026-10-13 09:00")},
		{"daily keeps wall time over DST", at(t, "America/New_York", "2026-11-01 09:00"), at(t, "America/New_York", "2026-11-01 09:30"), Recurrence{Freq: "daily", Interval: 1}, at(t, "America/New_York", "2026-11-02 09:00")},
		{"daily catches up after a long gap", at(t, "UTC", "2026-01-01 08:00"), at(t, "UTC", "2026-10-12 10:00"), Recurrence{Freq: "daily", Interval: 2}, at(t, "UTC", "2026-10-14 08:00")},
		{"weekly same weekday", at(t, "UTC", "2026-10-12 07:00"), at(t, "UTC", "2026-10-12 07:00"), Recurrence{Freq: "weekly", Interval: 1}, at(t, "UTC", "2026-10-19 07:00")},
		{"weekly weekdays", at(t, "UTC", "2026-10-12 07:00"), at(t, "UTC", "2026-10-12 07:00"), Recurrence{Freq: "weekly", Interval: 1, Weekdays: []int{1, 3, 5}}, at(t, "UTC", "2026-10-14 07:00")},
		{"weekly every other week", at(t, "UTC", "2026-10-12 07:00"), at(t, "UTC", "2026-10-14 08:00"), Recurrence{Freq: "weekly", Interval: 2, Weekdays: []int{1, 3}}, at(t, "UTC", "2026-10-26 07:00")},
		{"monthly clamps to month end", at(t, "UTC", "2026-01-31 09:00"), at(t, "UTC", "2026-01-31 09:00"), Recurrence{Freq: "monthly", Interval: 1}, at(t, "UTC", "2026-02-28 09:00")},
		{"monthly returns to the anchor day", at(t, "UTC", "2026-01-31 09:00"), at(t, "UTC", "2026-02-28 09:00"), Recurrence{Freq: "monthly", Interval: 1}, at(t, "UTC", "2026-03-31 09:00")},
	}
	for _, c := range cases {
		loc := time.UTC
		if c.name == "daily keeps wall time over DST" {
			loc = ny
		}
		if got := nextOccurrence(c.due, c.after, c.rec, loc); !got.Equal(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestRecurrenceNormalize(t *testing.T) {
	r := Recurrence{Freq: "daily"}
	if err := r.normalize(); err != nil || r.Interval != 1 {
		t.Fatalf("default interval: %v %d", err, r.Interval)
	}
	for _, bad := range []Recurrence{{Freq: "yearly"}, {Freq: "daily", Interval: 400}, {Freq: "daily", Weekdays: []int{1}}, {Freq: "weekly", Weekdays: []int{7}}, {Freq: "weekly", Weekdays: []int{1, 1}}} {
		if bad.normalize() == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}
