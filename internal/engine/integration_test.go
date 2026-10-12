package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/realnighthawk/life-engine/internal/engine"
	"github.com/realnighthawk/life-engine/internal/store/postgres"
)

func integrationService(t *testing.T) *engine.Service {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, e := pgxpool.New(context.Background(), url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	if e = postgres.Migrate(context.Background(), pool); e != nil {
		t.Fatal(e)
	}
	// One person per database, so tests start from an empty one.
	if _, e = pool.Exec(context.Background(), "TRUNCATE life.requests, life.changes, life.acked_events, life.reminders"); e != nil {
		t.Fatal(e)
	}
	return engine.New(pool)
}

func execute[T any](t *testing.T, s *engine.Service, name string, body any) T {
	t.Helper()
	raw, _ := json.Marshal(body)
	response, e := s.Execute(context.Background(), name, raw)
	if e != nil {
		t.Fatalf("%s: %v", name, engine.PublicError(e))
	}
	var v T
	if e = json.Unmarshal(response, &v); e != nil {
		t.Fatal(e)
	}
	return v
}

func errorCode(t *testing.T, s *engine.Service, name string, body any, code string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	_, e := s.Execute(context.Background(), name, raw)
	if e == nil || engine.PublicError(e).Code != code {
		t.Fatalf("%s: wanted %s, got %v", name, code, e)
	}
}

func key() string { return uuid.NewString() }

func ts(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

func TestReminderLifecycle(t *testing.T) {
	s := integrationService(t)
	due := ts(2 * time.Hour)
	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "  Pay rent ", "due_at": due, "time_zone": "Europe/London", "source": "agent", "session_id": "s1"})
	if r.CreatedAt.Location() != time.UTC || r.DueAt.Location() != time.UTC {
		t.Fatalf("timestamps must be UTC: %v %v", r.CreatedAt.Location(), r.DueAt.Location())
	}
	if r.Title != "Pay rent" || r.Version != 1 || r.State != "pending" || r.Source != "agent" || r.SessionID != "s1" {
		t.Fatalf("created: %+v", r)
	}

	// Retrying a write with the same key replays; a different input under that key conflicts.
	k := key()
	in := map[string]any{"idempotency_key": k, "title": "Call mum"}
	a := execute[engine.Reminder](t, s, "reminders_create", in)
	b := execute[engine.Reminder](t, s, "reminders_create", in)
	if a.ID != b.ID {
		t.Fatal("replay created a second record")
	}
	errorCode(t, s, "reminders_create", map[string]any{"idempotency_key": k, "title": "Different"}, "conflict")
	errorCode(t, s, "reminders_create", map[string]any{"title": "no key"}, "invalid_input")

	// Edits are versioned; a stale version is rejected.
	upd := map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 1, "title": "Pay the rent", "due_at": due}
	r2 := execute[engine.Reminder](t, s, "reminders_update", upd)
	if r2.Version != 2 || r2.Title != "Pay the rent" {
		t.Fatalf("updated: %+v", r2)
	}
	errorCode(t, s, "reminders_update", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 1, "title": "x"}, "conflict")

	// Snooze must be in the future; complete is final for a one-off.
	errorCode(t, s, "reminders_snooze", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 2, "until": ts(-time.Hour)}, "invalid_input")
	r3 := execute[engine.Reminder](t, s, "reminders_snooze", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 2, "until": ts(3 * time.Hour)})
	if r3.SnoozedUntil == nil || r3.Version != 3 {
		t.Fatalf("snoozed: %+v", r3)
	}
	r4 := execute[engine.Reminder](t, s, "reminders_complete", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 3})
	if r4.State != "done" || r4.CompletedAt == nil {
		t.Fatalf("completed: %+v", r4)
	}
	errorCode(t, s, "reminders_complete", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 4}, "conflict")

	// Lists default to pending and exclude deleted; delete and restore round trip.
	pending := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{})
	if pending.Total != 1 { // only "Call mum": the replayed create is one record and Pay rent is done
		t.Fatalf("pending total %d", pending.Total)
	}
	done := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{"status": "done"})
	if done.Total != 1 {
		t.Fatalf("done total %d", done.Total)
	}
	del := execute[engine.Reminder](t, s, "reminders_delete", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 1})
	if del.DeletedAt == nil {
		t.Fatal("not deleted")
	}
	if got := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{}); got.Total != 0 {
		t.Fatalf("deleted still listed: %d", got.Total)
	}
	execute[engine.Reminder](t, s, "reminders_restore", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 2})
	if got := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{"search": "mum"}); got.Total != 1 {
		t.Fatalf("search: %d", got.Total)
	}

	// Every change is audited, newest first.
	h := execute[engine.Page[engine.Change]](t, s, "history_list", map[string]any{"entity_type": "reminder", "id": r.ID})
	if h.Total != 4 || h.Items[0].Operation != "reminders_complete" {
		t.Fatalf("history: total %d first %s", h.Total, h.Items[0].Operation)
	}
}

func TestRecurringReminderAdvancesOnAck(t *testing.T) {
	s := integrationService(t)
	past := ts(-26 * time.Hour)
	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{
		"idempotency_key": key(), "title": "Take vitamins", "due_at": past, "time_zone": "UTC", "recurrence": map[string]any{"freq": "daily"}})
	errorCode(t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "bad", "recurrence": map[string]any{"freq": "daily"}}, "invalid_input") // needs due_at

	pending := execute[engine.EventsResult](t, s, "events_pending", map[string]any{})
	if len(pending.Items) != 1 {
		t.Fatalf("pending: %+v", pending)
	}
	ev := pending.Items[0]
	if ev.EventKey != "life:reminder" || ev.Notice.Title != "Take vitamins" || ev.Notice.Link.ID != r.ID {
		t.Fatalf("event: %+v", ev)
	}
	// Acking is idempotent and, for a repeating reminder, moves it to its next occurrence.
	if got := execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{ev.EventID}}); got.Acknowledged != 1 {
		t.Fatalf("ack: %+v", got)
	}
	if got := execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{ev.EventID}}); got.Acknowledged != 0 {
		t.Fatalf("second ack: %+v", got)
	}
	after := execute[engine.Reminder](t, s, "reminders_get", map[string]any{"id": r.ID})
	if after.DueAt == nil || !after.DueAt.After(time.Now()) || after.Version != 2 || after.State != "pending" {
		t.Fatalf("not advanced: %+v", after)
	}
	if again := execute[engine.EventsResult](t, s, "events_pending", map[string]any{}); len(again.Items) != 0 {
		t.Fatalf("still pending: %+v", again)
	}
}

func TestOneOffStaysPendingUntilCompletedAndSnoozeRenotifies(t *testing.T) {
	s := integrationService(t)
	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Renew passport", "due_at": ts(-time.Minute)})
	first := execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items
	if len(first) != 1 {
		t.Fatal("expected one notice")
	}
	execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{first[0].EventID}})
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("acked notice returned again: %d", n)
	}
	got := execute[engine.Reminder](t, s, "reminders_get", map[string]any{"id": r.ID})
	if got.State != "pending" || got.Version != 1 {
		t.Fatalf("a one-off must stay pending after firing: %+v", got)
	}
	// An edited due time is a new occurrence and notifies again.
	execute[engine.Reminder](t, s, "reminders_update", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 1, "title": "Renew passport", "due_at": ts(-30 * time.Second)})
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 1 {
		t.Fatalf("edited reminder should notify again, got %d", n)
	}
	// A deleted reminder never notifies.
	execute[engine.Reminder](t, s, "reminders_delete", map[string]any{"idempotency_key": key(), "id": r.ID, "expected_version": 2})
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("deleted reminder notified: %d", n)
	}
}

func TestValidation(t *testing.T) {
	s := integrationService(t)
	for name, body := range map[string]map[string]any{
		"empty title":  {"idempotency_key": key(), "title": "   "},
		"bad zone":     {"idempotency_key": key(), "title": "x", "time_zone": "Mars/Base"},
		"bad due":      {"idempotency_key": key(), "title": "x", "due_at": "tomorrow"},
		"bad source":   {"idempotency_key": key(), "title": "x", "source": "robot"},
		"bad link":     {"idempotency_key": key(), "title": "x", "link": map[string]any{"entity": "pizza", "id": "1"}},
		"unknown prop": {"idempotency_key": key(), "title": "x", "color": "red"},
	} {
		raw, _ := json.Marshal(body)
		if _, e := s.Execute(context.Background(), "reminders_create", raw); e == nil || engine.PublicError(e).Code != "invalid_input" {
			t.Errorf("%s: want invalid_input, got %v", name, e)
		}
	}
	errorCode(t, s, "reminders_get", map[string]any{"id": uuid.NewString()}, "not_found")
	errorCode(t, s, "nope", map[string]any{}, "not_found")
}
