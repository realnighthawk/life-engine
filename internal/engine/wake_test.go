package engine

import (
	"testing"
	"time"
)

func TestDesiredWake(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	daily := &Recurrence{Freq: "daily"}
	cases := []struct {
		name string
		r    wakeRow
		want *time.Time
	}{
		{"future", wakeRow{state: "pending", tz: "UTC", due: at(time.Hour)}, at(time.Hour)},
		{"undated", wakeRow{state: "pending", tz: "UTC"}, nil},
		{"done", wakeRow{state: "done", tz: "UTC", due: at(time.Hour)}, nil},
		{"deleted", wakeRow{state: "pending", tz: "UTC", due: at(time.Hour), deleted: true}, nil},
		{"snooze wins", wakeRow{state: "pending", tz: "UTC", due: at(time.Hour), snoozed: at(3 * time.Hour)}, at(3 * time.Hour)},
		{"just missed fires now", wakeRow{state: "pending", tz: "UTC", due: at(-time.Minute)}, at(2 * time.Second)},
		{"long overdue stays unarmed", wakeRow{state: "pending", tz: "UTC", due: at(-time.Hour)}, nil},
		{"already fired keeps its arming", wakeRow{state: "pending", tz: "UTC", due: at(-time.Minute), armedFor: at(-time.Minute)}, at(-time.Minute)},
		{"recurring moves to the next occurrence", wakeRow{state: "pending", tz: "UTC", due: at(-time.Minute), rec: daily, armedFor: at(-time.Minute)}, at(24*time.Hour - time.Minute)},
	}
	for _, c := range cases {
		got := desiredWake(c.r, now)
		if !same(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
