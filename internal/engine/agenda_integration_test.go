package engine_test

import (
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestAgendaListsEachOccurrenceAndClearsThemOneByOne(t *testing.T) {
	s := integrationService(t)
	now := time.Now().UTC()
	if now.Hour() == 23 && now.Minute() >= 58 || now.Hour() == 0 && now.Minute() < 2 {
		t.Skip("too close to midnight for fixed times of day")
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	at := func(h, m int, back int) string {
		return midnight.AddDate(0, 0, -back).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Format(time.RFC3339)
	}

	// Two repeating reminders created a day ago: one that has already fired today (00:01) and one still to come (23:59).
	early := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Early water", "due_at": at(0, 1, 1), "time_zone": "UTC", "recurrence": map[string]any{"freq": "daily"}})
	late := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Late water", "due_at": at(23, 59, 1), "time_zone": "UTC", "recurrence": map[string]any{"freq": "daily"}})
	// A one-off from three days ago that was never cleared, and one earlier today.
	old := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Renew passport", "due_at": at(9, 0, 3)})
	once := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Call Sam", "due_at": at(0, 2, 0)})

	find := func(a engine.Agenda, id string) *engine.AgendaItem {
		for i := range a.Items {
			if a.Items[i].Reminder.ID == id {
				return &a.Items[i]
			}
		}
		return nil
	}
	a := execute[engine.Agenda](t, s, "reminders_agenda", map[string]any{})
	if a.Day != now.Format("2006-01-02") {
		t.Fatalf("day %s", a.Day)
	}
	if i := find(a, early.ID); i == nil || i.State != "due" || i.Overdue {
		t.Fatalf("a repeating cue that fired earlier today is due: %+v", i)
	}
	if i := find(a, late.ID); i == nil || i.State != "upcoming" {
		t.Fatalf("a cue later today is upcoming: %+v", i)
	}
	if i := find(a, old.ID); i == nil || i.State != "due" || !i.Overdue {
		t.Fatalf("an open one-off from an earlier day is carried over, overdue: %+v", i)
	}
	for k := 1; k < len(a.Items); k++ {
		if a.Items[k].At.Before(a.Items[k-1].At) {
			t.Fatal("items must be soonest first")
		}
	}

	// Clearing today's early occurrence does not touch the series or its version, nor tomorrow's.
	cleared := execute[engine.Reminder](t, s, "reminders_ack", map[string]any{"idempotency_key": key(), "id": early.ID, "at": at(0, 1, 0)})
	if cleared.State != "pending" || cleared.Version != early.Version {
		t.Fatalf("a repeating reminder carries on and is not edited: %+v", cleared)
	}
	again := execute[engine.Reminder](t, s, "reminders_ack", map[string]any{"idempotency_key": key(), "id": early.ID, "at": at(0, 1, 0)})
	if again.Version != early.Version {
		t.Fatal("clearing the same occurrence twice must change nothing")
	}
	a = execute[engine.Agenda](t, s, "reminders_agenda", map[string]any{})
	if i := find(a, early.ID); i == nil || i.State != "acknowledged" {
		t.Fatalf("cleared: %+v", i)
	}
	if i := find(a, late.ID); i == nil || i.State != "upcoming" {
		t.Fatalf("the other series is untouched: %+v", i)
	}
	tomorrow := execute[engine.Agenda](t, s, "reminders_agenda", map[string]any{"day": now.AddDate(0, 0, 1).Format("2006-01-02")})
	if i := find(tomorrow, early.ID); i == nil || i.State != "upcoming" {
		t.Fatalf("tomorrow's occurrence is a separate one: %+v", i)
	}

	// A one-off is finished by clearing it, and then no longer carried over.
	done := execute[engine.Reminder](t, s, "reminders_ack", map[string]any{"idempotency_key": key(), "id": once.ID})
	if done.State != "done" || done.CompletedAt == nil {
		t.Fatalf("one-off: %+v", done)
	}
	execute[engine.Reminder](t, s, "reminders_ack", map[string]any{"idempotency_key": key(), "id": old.ID})
	a = execute[engine.Agenda](t, s, "reminders_agenda", map[string]any{})
	if i := find(a, old.ID); i != nil && i.State != "acknowledged" {
		t.Fatalf("a finished one-off must not stay open: %+v", i)
	}
	if i := find(a, once.ID); i == nil || i.State != "acknowledged" {
		t.Fatalf("today's finished one-off shows as acknowledged: %+v", i)
	}
	errorCode(t, s, "reminders_ack", map[string]any{"idempotency_key": key(), "id": "ffffffff-ffff-4fff-8fff-ffffffffffff"}, "not_found")
}
