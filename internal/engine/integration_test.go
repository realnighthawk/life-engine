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
	if _, e = pool.Exec(context.Background(), "TRUNCATE life.requests, life.changes, life.acked_events, life.notices, life.occurrences, life.items, life.goals, life.views, life.measurements"); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(context.Background(), "UPDATE life.settings SET time_zone='UTC',work_start='09:00',work_end='17:00',version=1"); e != nil {
		t.Fatal(e)
	}
	return engine.New(pool), pool
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

func TestGoalLifecycleAndProgressAreDerived(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "  Lisbon trip ", "ends_on": "2026-06-10", "notes": "take the coolers"})
	if g.Title != "Lisbon trip" || g.Subject != "person" || g.EndsOn == nil || *g.EndsOn != "2026-06-10" || g.Progress.Total != 0 {
		t.Fatalf("created: %+v", g)
	}
	t1 := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Renew passport", "goal_id": g.ID, "at": ts(48 * time.Hour)})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Book flights", "goal_id": g.ID, "at": ts(24 * time.Hour)})
	execute[engine.Item](t, s, "calendar_events_create", map[string]any{"idempotency_key": key(), "title": "Flight", "goal_id": g.ID, "at": ts(72 * time.Hour), "minutes": 150})

	got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if got.Progress.Done != 0 || got.Progress.Total != 2 { // the event is not counted: it happens, it is not done
		t.Fatalf("progress: %+v", got.Progress)
	}
	if got.Next == nil || got.Next.Title != "Book flights" {
		t.Fatalf("next is the soonest open task: %+v", got.Next)
	}
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": t1.ID, "expected_version": 1})
	got = execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if got.Progress.Done != 1 || got.Progress.Total != 2 {
		t.Fatalf("progress after completing: %+v", got.Progress)
	}

	// Archiving keeps what is linked; a link to an archived goal is refused.
	arch := execute[engine.Goal](t, s, "goals_archive", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": 1})
	if arch.DeletedAt == nil {
		t.Fatal("not archived")
	}
	errorCode(t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "goal_id": g.ID}, "invalid_input")
	execute[engine.Goal](t, s, "goals_restore", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": 2})
	errorCode(t, s, "goals_update", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": 1, "title": "x"}, "conflict")
}

func TestTaskLifecycle(t *testing.T) {
	s := integrationService(t)
	due := ts(2 * time.Hour)
	task := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "  Pay rent ", "at": due, "time_zone": "Europe/London", "alert_before_minutes": 15})
	if task.Title != "Pay rent" || task.Kind != "task" || task.Version != 1 || task.At.Location() != time.UTC || *task.AlertBeforeMinutes != 15 {
		t.Fatalf("created: %+v", task)
	}
	// Retrying a write with the same key replays; a different input under that key conflicts.
	k := key()
	in := map[string]any{"idempotency_key": k, "title": "Call mum"}
	a := execute[engine.Item](t, s, "tasks_create", in)
	b := execute[engine.Item](t, s, "tasks_create", in)
	if a.ID != b.ID {
		t.Fatal("replay created a second record")
	}
	errorCode(t, s, "tasks_create", map[string]any{"idempotency_key": k, "title": "Different"}, "conflict")
	errorCode(t, s, "tasks_create", map[string]any{"title": "no key"}, "invalid_input")

	// Edits are versioned; changing the time clears a snooze.
	snoozed := execute[engine.Item](t, s, "tasks_snooze", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "until": ts(3 * time.Hour)})
	if snoozed.SnoozedUntil == nil || snoozed.Version != 2 {
		t.Fatalf("snoozed: %+v", snoozed)
	}
	errorCode(t, s, "tasks_snooze", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 2, "until": ts(-time.Hour)}, "invalid_input")
	errorCode(t, s, "tasks_snooze", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 1, "until": ts(time.Hour)}, "invalid_input") // no alert to snooze
	upd := execute[engine.Item](t, s, "tasks_update", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 2, "title": "Pay the rent", "at": due, "alert_before_minutes": 15})
	if upd.Version != 3 || upd.SnoozedUntil != nil || upd.Title != "Pay the rent" {
		t.Fatalf("updated: %+v", upd)
	}
	errorCode(t, s, "tasks_update", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "title": "x"}, "conflict")

	// A one-off is finished once.
	done := execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 3})
	if done.DoneAt == nil {
		t.Fatalf("completed: %+v", done)
	}
	errorCode(t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 4}, "conflict")
	// Done can be taken back; reopening what is open is refused.
	reopened := execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 4, "done": false})
	if reopened.DoneAt != nil || reopened.Version != 5 {
		t.Fatalf("reopened: %+v", reopened)
	}
	errorCode(t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 5, "done": false}, "conflict")
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 5})

	// Lists default to open and exclude deleted; delete and restore round trip.
	if open := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{}); open.Total != 1 { // Call mum; Pay rent is done
		t.Fatalf("open total %d", open.Total)
	}
	if all := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{"status": "done"}); all.Total != 1 {
		t.Fatalf("done total %d", all.Total)
	}
	execute[engine.Item](t, s, "tasks_delete", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 1})
	if got := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{}); got.Total != 0 {
		t.Fatalf("deleted still listed: %d", got.Total)
	}
	execute[engine.Item](t, s, "tasks_restore", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 2})
	if got := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{"search": "mum"}); got.Total != 1 {
		t.Fatalf("search: %d", got.Total)
	}
	// A task and an event are different families: neither can be read through the other.
	errorCode(t, s, "calendar_events_get", map[string]any{"id": task.ID}, "not_found")

	h := execute[engine.Page[engine.Change]](t, s, "history_list", map[string]any{"entity_type": "item", "id": task.ID})
	if h.Total != 6 || h.Items[0].Operation != "tasks_complete" {
		t.Fatalf("history: total %d first %s", h.Total, h.Items[0].Operation)
	}
}

func TestValidation(t *testing.T) {
	s := integrationService(t)
	for name, c := range map[string]struct {
		op   string
		body map[string]any
	}{
		"empty title":                           {"tasks_create", map[string]any{"idempotency_key": key(), "title": "   "}},
		"bad zone":                              {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "time_zone": "Mars/Base"}},
		"bad time":                              {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "at": "tomorrow"}},
		"bad subject":                           {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "subject": "robot"}},
		"recurrence needs a time":               {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "recurrence": map[string]any{"freq": "daily"}}},
		"alert needs a time":                    {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "alert_before_minutes": 5}},
		"an event needs a time":                 {"calendar_events_create", map[string]any{"idempotency_key": key(), "title": "x"}},
		"unknown goal":                          {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "goal_id": uuid.NewString()}},
		"unknown property":                      {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "color": "red"}},
		"alert across midnight":                 {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "at": "2026-10-12T00:10:00Z", "recurrence": map[string]any{"freq": "daily"}, "alert_before_minutes": 30}},
		"alert on biweekly days":                {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "at": "2026-10-12T09:00:00Z", "recurrence": map[string]any{"freq": "weekly", "interval": 2, "weekdays": []int{1, 3}}, "alert_before_minutes": 0}},
		"an agent's alert needs a conversation": {"tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "subject": "agent", "at": ts(time.Hour), "alert_before_minutes": 0}},
	} {
		raw, _ := json.Marshal(c.body)
		if _, e := s.Execute(context.Background(), c.op, raw); e == nil || engine.PublicError(e).Code != "invalid_input" {
			t.Errorf("%s: want invalid_input, got %v", name, e)
		}
	}
	errorCode(t, s, "tasks_get", map[string]any{"id": uuid.NewString()}, "not_found")
	errorCode(t, s, "nope", map[string]any{}, "not_found")
}

// The agent's own items are not the person's: lists and the day default to the person.
func TestAgentItemsStayOutOfThePersonsScreens(t *testing.T) {
	s := integrationService(t)
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Mine", "at": ts(time.Hour)})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Agent watch", "subject": "agent", "at": ts(time.Hour)})
	if got := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{}); got.Total != 1 || got.Items[0].Title != "Mine" {
		t.Fatalf("person's list: %+v", got.Items)
	}
	if got := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{"subject": "agent"}); got.Total != 1 || got.Items[0].Title != "Agent watch" {
		t.Fatalf("agent's list: %+v", got.Items)
	}
	execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Keep inbox triaged", "subject": "agent"})
	if got := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{}); got.Total != 0 {
		t.Fatalf("person's goals include the agent's: %d", got.Total)
	}
}

func mustUnmarshal(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}
