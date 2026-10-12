package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestSettings(t *testing.T) {
	s := integrationService(t)
	cur := execute[engine.Settings](t, s, "settings_get", map[string]any{})
	if cur.TimeZone != "UTC" || cur.Version != 1 {
		t.Fatalf("defaults: %+v", cur)
	}
	set := map[string]any{"idempotency_key": key(), "expected_version": 1, "time_zone": "Europe/London", "work_start": "08:30", "work_end": "16:30"}
	if got := execute[engine.Settings](t, s, "settings_set", set); got.Version != 2 || got.TimeZone != "Europe/London" {
		t.Fatalf("set: %+v", got)
	}
	errorCode(t, s, "settings_set", map[string]any{"idempotency_key": key(), "expected_version": 1, "time_zone": "UTC", "work_start": "09:00", "work_end": "17:00"}, "conflict")
	errorCode(t, s, "settings_set", map[string]any{"idempotency_key": key(), "expected_version": 2, "time_zone": "Nowhere/Land", "work_start": "09:00", "work_end": "17:00"}, "invalid_input")
	errorCode(t, s, "settings_set", map[string]any{"idempotency_key": key(), "expected_version": 2, "time_zone": "UTC", "work_start": "18:00", "work_end": "17:00"}, "invalid_input")
}

func TestScheduleLifecycleAndEvents(t *testing.T) {
	s, pool := integrationEnv(t)
	execute[engine.Settings](t, s, "settings_set", map[string]any{"idempotency_key": key(), "expected_version": 1, "time_zone": "America/New_York", "work_start": "09:00", "work_end": "17:00"})
	sc := execute[engine.Schedule](t, s, "schedules_create", map[string]any{"idempotency_key": key(), "name": "Weekly review", "instruction": "Review my week and tell me what to change.",
		"time_of_day": "18:00", "recurrence": map[string]any{"freq": "weekly", "weekdays": []int{0}}})
	if sc.TimeZone != "America/New_York" || !sc.Enabled || !sc.NextAt.After(time.Now()) || sc.NextAt.Weekday() != time.Sunday && sc.NextAt.In(mustZone(t, sc.TimeZone)).Weekday() != time.Sunday {
		t.Fatalf("created: %+v", sc)
	}
	if h := sc.NextAt.In(mustZone(t, sc.TimeZone)).Hour(); h != 18 {
		t.Fatalf("fires at local hour %d", h)
	}
	errorCode(t, s, "schedules_create", map[string]any{"idempotency_key": key(), "name": "weekly REVIEW", "instruction": "x", "time_of_day": "09:00", "recurrence": map[string]any{"freq": "daily"}}, "conflict")
	errorCode(t, s, "schedules_create", map[string]any{"idempotency_key": key(), "name": "Bad", "instruction": "x", "time_of_day": "25:00", "recurrence": map[string]any{"freq": "daily"}}, "invalid_input")

	// It fires from a wake. Move the occurrence into the past (beyond the grace window) and the sync advances the row to the
	// next occurrence, recording the one that fired, and arms that.
	f := &fakeWaker{armed: map[string]engine.Wake{}}
	s.UseWaker(f)
	if e := s.SyncWakes(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w, ok := f.armed["schedule-"+sc.ID]; !ok || !w.At.Equal(sc.NextAt) {
		t.Fatalf("not armed for next_at: %+v", f.armed)
	}
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("a schedule must not be a feed event: %d", n)
	}
	if _, e := pool.Exec(context.Background(), "UPDATE life.schedules SET next_at = now() - interval '10 minutes' WHERE id=$1", sc.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.SyncWakes(context.Background()); e != nil {
		t.Fatal(e)
	}
	after := execute[engine.Schedule](t, s, "schedules_get", map[string]any{"id": sc.ID})
	if !after.NextAt.After(time.Now()) || after.LastFiredAt == nil || !f.armed["schedule-"+sc.ID].At.Equal(after.NextAt) {
		t.Fatalf("not advanced: %+v", after)
	}

	// A disabled or deleted schedule never fires; a restored one resumes from now.
	upd := execute[engine.Schedule](t, s, "schedules_update", map[string]any{"idempotency_key": key(), "id": sc.ID, "expected_version": after.Version, "name": "Weekly review",
		"instruction": "Review my week.", "time_of_day": "18:00", "recurrence": map[string]any{"freq": "weekly", "weekdays": []int{0}}, "enabled": false})
	if e := s.SyncWakes(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, armed := f.armed["schedule-"+sc.ID]; upd.Enabled || armed {
		t.Fatalf("disabled schedule still has a wake: %+v", upd)
	}
	del := execute[engine.Schedule](t, s, "schedules_delete", map[string]any{"idempotency_key": key(), "id": sc.ID, "expected_version": upd.Version})
	res := execute[engine.Schedule](t, s, "schedules_restore", map[string]any{"idempotency_key": key(), "id": sc.ID, "expected_version": del.Version})
	if res.DeletedAt != nil || !res.NextAt.After(time.Now()) {
		t.Fatalf("restore: %+v", res)
	}
	if list := execute[engine.Page[engine.Schedule]](t, s, "schedules_list", map[string]any{}); list.Total != 1 {
		t.Fatalf("list: %d", list.Total)
	}
	h := execute[engine.Page[engine.Change]](t, s, "history_list", map[string]any{"entity_type": "schedule", "id": sc.ID})
	if h.Total < 4 { // create, edit, delete, restore; the wake moving next_at on is bookkeeping, not an edit
		t.Fatalf("history: %d", h.Total)
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
