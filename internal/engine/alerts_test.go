package engine

import (
	"testing"
	"time"
)

func item(t *testing.T, at time.Time, before int, rec *Recurrence, zoneName string) scheduledItem {
	t.Helper()
	return scheduledItem{ID: "i1", Kind: "task", Subject: "person", At: &at, Alert: &before, Recurrence: rec, TimeZone: zoneName}
}

func TestAnAlertWithoutATimeOrWithoutAnAlertArmsNothing(t *testing.T) {
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	for name, it := range map[string]scheduledItem{
		"no alert": {At: &now, TimeZone: "UTC"},
		"no time":  {Alert: new(int), TimeZone: "UTC"},
	} {
		if specs, err := desiredAlerts(it, now); err != nil || len(specs) != 0 {
			t.Errorf("%s: %v %v", name, specs, err)
		}
	}
	it := item(t, now.Add(time.Hour), 0, nil, "UTC")
	done := now
	it.DoneAt = &done
	if specs, _ := desiredAlerts(it, now); len(specs) != 0 {
		t.Errorf("a done task is not alerted: %v", specs)
	}
	del := item(t, now.Add(time.Hour), 0, nil, "UTC")
	del.DeletedAt = &now
	if specs, _ := desiredAlerts(del, now); len(specs) != 0 {
		t.Errorf("a deleted item is not alerted: %v", specs)
	}
}

func TestAOneOffAlertFiresBeforeItsTimeAndIsLeftOnceGone(t *testing.T) {
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	specs, err := desiredAlerts(item(t, now.Add(time.Hour), 15, nil, "UTC"), now)
	if err != nil || len(specs) != 1 || specs[0].Once == nil || !specs[0].Once.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("a quarter hour ahead: %+v %v", specs, err)
	}
	if specs, _ = desiredAlerts(item(t, now.Add(-time.Hour), 0, nil, "UTC"), now); len(specs) != 0 {
		t.Fatalf("an alert an hour past is left: %+v", specs)
	}
	if specs, _ = desiredAlerts(item(t, now.Add(-time.Minute), 0, nil, "UTC"), now); len(specs) != 1 {
		t.Fatalf("an alert a minute past is still armed: %+v", specs)
	}
	snoozed := item(t, now.Add(time.Hour), 15, nil, "UTC")
	until := now.Add(3 * time.Hour)
	snoozed.SnoozedUntil = &until
	if specs, _ = desiredAlerts(snoozed, now); len(specs) != 1 || !specs[0].Once.Equal(until) {
		t.Fatalf("a snooze moves a one-off's alert: %+v", specs)
	}
}

func TestARepeatingAlertKeepsLocalWallClockTime(t *testing.T) {
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	// 09:00 London, alerting 30 minutes before: 08:30 local, every day.
	london := at(t, "Europe/London", "2026-10-12 09:00")
	specs, err := desiredAlerts(item(t, london, 30, &Recurrence{Freq: "daily", Interval: 1}, "Europe/London"), now)
	if err != nil || len(specs) != 1 || specs[0].Calendar == nil {
		t.Fatalf("daily: %+v %v", specs, err)
	}
	c := specs[0].Calendar
	if c.Hour != 8 || c.Minute != 30 || specs[0].TimeZone != "Europe/London" || len(c.Weekdays) != 0 {
		t.Fatalf("daily calendar: %+v", specs[0])
	}
	// Weekly on chosen weekdays, and on the anchor's weekday when none are named.
	specs, _ = desiredAlerts(item(t, london, 0, &Recurrence{Freq: "weekly", Interval: 1, Weekdays: []int{1, 3}}, "Europe/London"), now)
	if got := specs[0].Calendar.Weekdays; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("weekdays: %+v", specs[0].Calendar)
	}
	specs, _ = desiredAlerts(item(t, london, 0, &Recurrence{Freq: "weekly", Interval: 1}, "Europe/London"), now)
	if got := specs[0].Calendar.Weekdays; len(got) != 1 || got[0] != int(time.Monday) {
		t.Fatalf("the anchor's weekday: %+v", specs[0].Calendar)
	}
	// Monthly on the anchor's day.
	specs, _ = desiredAlerts(item(t, london, 0, &Recurrence{Freq: "monthly", Interval: 1}, "Europe/London"), now)
	if specs[0].Calendar.DayOfMonth != 12 {
		t.Fatalf("monthly: %+v", specs[0].Calendar)
	}
	// Every other day is a fixed interval aligned to the first alert.
	specs, _ = desiredAlerts(item(t, london, 0, &Recurrence{Freq: "daily", Interval: 2}, "Europe/London"), now)
	if iv := specs[0].Interval; iv == nil || iv.Every != 48*time.Hour || iv.Offset != time.Duration(london.Unix()%(48*3600))*time.Second {
		t.Fatalf("interval: %+v", specs[0])
	}
	// A snooze adds a one-shot beside the series.
	snoozed := item(t, london, 0, &Recurrence{Freq: "daily", Interval: 1}, "Europe/London")
	until := now.Add(time.Hour)
	snoozed.SnoozedUntil = &until
	if specs, _ = desiredAlerts(snoozed, now); len(specs) != 2 || specs[1].Name != "snooze" || !specs[1].Once.Equal(until) {
		t.Fatalf("snooze beside the series: %+v", specs)
	}
}

func TestARepeatingAlertTheClockCannotKeepIsRefused(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	nine := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		at     time.Time
		before int
		rec    Recurrence
	}{
		"across midnight":         {time.Date(2026, 10, 12, 0, 10, 0, 0, time.UTC), 30, Recurrence{Freq: "daily", Interval: 1}},
		"every other week + days": {nine, 0, Recurrence{Freq: "weekly", Interval: 2, Weekdays: []int{1, 3}}},
		"every other month":       {nine, 0, Recurrence{Freq: "monthly", Interval: 2}},
	} {
		if _, err := recurringAlert(c.at, c.before, c.rec, loc); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
	// What can be said is accepted.
	for name, rec := range map[string]Recurrence{
		"every other week": {Freq: "weekly", Interval: 2},
		"every third day":  {Freq: "daily", Interval: 3},
	} {
		if _, err := recurringAlert(nine, 0, rec, loc); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheSpecHashChangesWithTheSpecAndOnlyThen(t *testing.T) {
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	a, _ := desiredAlerts(item(t, now.Add(time.Hour), 15, nil, "UTC"), now)
	b, _ := desiredAlerts(item(t, now.Add(time.Hour), 15, nil, "UTC"), now)
	c, _ := desiredAlerts(item(t, now.Add(2*time.Hour), 15, nil, "UTC"), now)
	if hashSpecs(a) != hashSpecs(b) || hashSpecs(a) == hashSpecs(c) || hashSpecs(nil) != "" {
		t.Fatal("hash")
	}
}
