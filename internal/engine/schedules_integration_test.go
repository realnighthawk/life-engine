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

	// Nothing is due yet; move the occurrence into the past and it fires once, then advances on ack.
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("early notice: %d", n)
	}
	if _, e := pool.Exec(context.Background(), "UPDATE life.schedules SET next_at = now() - interval '1 minute' WHERE id=$1", sc.ID); e != nil {
		t.Fatal(e)
	}
	pending := execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items
	if len(pending) != 1 || pending[0].EventKey != "life:schedule" || pending[0].Notice.Title != "Weekly review" {
		t.Fatalf("pending: %+v", pending)
	}
	execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{pending[0].EventID}})
	after := execute[engine.Schedule](t, s, "schedules_get", map[string]any{"id": sc.ID})
	if !after.NextAt.After(time.Now()) || after.LastFiredAt == nil {
		t.Fatalf("not advanced: %+v", after)
	}
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("re-notified: %d", n)
	}

	// A disabled or deleted schedule never fires; a restored one resumes from now.
	pool.Exec(context.Background(), "UPDATE life.schedules SET next_at = now() - interval '1 minute' WHERE id=$1", sc.ID)
	upd := execute[engine.Schedule](t, s, "schedules_update", map[string]any{"idempotency_key": key(), "id": sc.ID, "expected_version": after.Version, "name": "Weekly review",
		"instruction": "Review my week.", "time_of_day": "18:00", "recurrence": map[string]any{"freq": "weekly", "weekdays": []int{0}}, "enabled": false})
	if upd.Enabled || len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items) != 0 {
		t.Fatalf("disabled schedule fired: %+v", upd)
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
	if h.Total < 5 {
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
