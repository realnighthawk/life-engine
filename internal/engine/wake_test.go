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
		{"recurring waits out the grace window", wakeRow{state: "pending", tz: "UTC", due: at(-time.Minute), rec: daily, armedFor: at(-time.Minute)}, at(-time.Minute)},
		{"recurring moves to the next occurrence", wakeRow{state: "pending", tz: "UTC", due: at(-5 * time.Minute), rec: daily, armedFor: at(-5 * time.Minute)}, at(24*time.Hour - 5*time.Minute)},
	}
	for _, c := range cases {
		got := desiredWake(c.r, now)
		if !same(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestDesiredRoutineWake(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) // a Monday
	clock := func(v string) *string { return &v }
	cases := []struct {
		name string
		r    routineRow
		want string
	}{
		{"later today", routineRow{remindAt: clock("18:30"), tz: "UTC"}, "2026-10-12T18:30:00Z"},
		{"already passed, so tomorrow", routineRow{remindAt: clock("08:00"), tz: "UTC"}, "2026-10-13T08:00:00Z"},
		{"only on Wednesdays", routineRow{remindAt: clock("08:00"), tz: "UTC", weekdays: []int16{3}}, "2026-10-14T08:00:00Z"},
		{"on the local clock", routineRow{remindAt: clock("07:00"), tz: "America/New_York"}, "2026-10-13T11:00:00Z"},
		{"just due stays", routineRow{remindAt: clock("11:59"), tz: "UTC"}, "2026-10-12T11:59:00Z"},
	}
	for _, c := range cases {
		got := desiredRoutineWake(c.r, now)
		if got == nil || got.Format(time.RFC3339) != c.want {
			t.Errorf("%s: got %v want %s", c.name, got, c.want)
		}
	}
	if desiredRoutineWake(routineRow{tz: "UTC"}, now) != nil {
		t.Error("no remind_at means no wake")
	}
}
