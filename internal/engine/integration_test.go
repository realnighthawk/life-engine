package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/realnighthawk/life-engine/internal/blobs"
	"github.com/realnighthawk/life-engine/internal/engine"
	"github.com/realnighthawk/life-engine/internal/store/postgres"
)

func integrationService(t *testing.T) *engine.Service {
	t.Helper()
	s, _ := integrationEnv(t)
	return s
}

// integrationEnv also returns the pool, for tests that must move time (set a due instant in the past).
func integrationEnv(t *testing.T) (*engine.Service, *pgxpool.Pool) {
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
	if _, e = pool.Exec(context.Background(), "TRUNCATE life.requests, life.changes, life.acked_events, life.reminders, life.schedules, life.attachments, life.captures, life.notes, life.journal_entries, life.routine_checkins, life.routines, life.plan_proposals, life.plan_items, life.commitments, life.steps, life.goals"); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(context.Background(), "UPDATE life.settings SET time_zone='UTC',work_start='09:00',work_end='17:00',version=1"); e != nil {
		t.Fatal(e)
	}
	return engine.New(pool, blobs.NewMemory()), pool
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

func TestRecurringReminderNeedsADueTime(t *testing.T) {
	s := integrationService(t)
	errorCode(t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "bad", "recurrence": map[string]any{"freq": "daily"}}, "invalid_input")
}

// Reminders no longer appear in events_pending: they fire from a Temporal Schedule (wake_integration_test.go).
func TestDueReminderIsNotAFeedEvent(t *testing.T) {
	s := integrationService(t)
	execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Renew passport", "due_at": ts(-time.Minute)})
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("a due reminder must not be a feed event: %d", n)
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
