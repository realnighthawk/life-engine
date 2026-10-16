package engine_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/realnighthawk/life-engine/internal/engine"
)

type fakeWaker struct {
	mu    sync.Mutex
	armed map[string]engine.Wake
	log   []string
}

func (f *fakeWaker) Arm(_ context.Context, w engine.Wake) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed[w.Name] = w
	f.log = append(f.log, "arm "+w.Name)
	return nil
}
func (f *fakeWaker) Cancel(_ context.Context, session, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Like Temporal: a wake is identified by session and name, so only one armed on that session is removed.
	if w, ok := f.armed[name]; ok && w.SessionKey == session {
		delete(f.armed, name)
	}
	f.log = append(f.log, "cancel "+session+"/"+name)
	return nil
}

func (f *fakeWaker) Exists(_ context.Context, _, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.armed[name]
	return ok, nil
}

func newSynced(t *testing.T) (*engine.Service, *fakeWaker, func()) {
	t.Helper()
	s, f, sync, _ := newSyncedPool(t)
	return s, f, sync
}

func newSyncedPool(t *testing.T) (*engine.Service, *fakeWaker, func(), *pgxpool.Pool) {
	t.Helper()
	s, pool := integrationEnv(t)
	f := &fakeWaker{armed: map[string]engine.Wake{}}
	s.UseWaker(f)
	return s, f, func() {
		t.Helper()
		if e := s.SyncWakes(context.Background()); e != nil {
			t.Fatal(e)
		}
	}, pool
}

func TestReminderWakeFollowsTheRow(t *testing.T) {
	s, _ := integrationEnv(t)
	f := &fakeWaker{armed: map[string]engine.Wake{}}
	s.UseWaker(f)
	sync := func() {
		t.Helper()
		if e := s.SyncWakes(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	due := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": "w1", "title": "Call Sam", "due_at": due.Format(time.RFC3339)})
	sync()
	name := "reminder-" + r.ID
	w, ok := f.armed[name]
	if !ok || !w.At.Equal(due) || w.Subject != "reminder:"+r.ID || w.Kind != "reminder" {
		t.Fatalf("not armed for the due time: %+v", f.armed)
	}
	n := len(f.log)
	sync()
	if len(f.log) != n {
		t.Fatalf("a second sync with nothing changed must do nothing: %v", f.log[n:])
	}

	later := due.Add(time.Hour)
	execute[engine.Reminder](t, s, "reminders_snooze", map[string]any{"idempotency_key": "w2", "id": r.ID, "expected_version": r.Version, "until": later.Format(time.RFC3339)})
	sync()
	if w = f.armed[name]; !w.At.Equal(later) {
		t.Fatalf("snooze must retime the wake: %v", w.At)
	}

	cur := execute[engine.Reminder](t, s, "reminders_get", map[string]any{"id": r.ID})
	execute[engine.Reminder](t, s, "reminders_complete", map[string]any{"idempotency_key": "w3", "id": r.ID, "expected_version": cur.Version})
	sync()
	if _, ok = f.armed[name]; ok {
		t.Fatal("a completed reminder must have no wake")
	}
}

func TestScheduleWakeFollowsTheRowAndMovesOn(t *testing.T) {
	s, f, sync := newSynced(t)
	sc := execute[engine.Schedule](t, s, "schedules_create", map[string]any{"idempotency_key": "s1", "name": "Weekly review", "instruction": "Review my week",
		"time_of_day": "09:00", "time_zone": "UTC", "recurrence": map[string]any{"freq": "weekly"}})
	sync()
	w, ok := f.armed["schedule-"+sc.ID]
	if !ok || !w.At.Equal(sc.NextAt) || w.Kind != "instruction" || w.Subject != "schedule:"+sc.ID {
		t.Fatalf("not armed for next_at %v: %+v", sc.NextAt, f.armed)
	}
	execute[engine.Schedule](t, s, "schedules_update", map[string]any{"idempotency_key": "s2", "id": sc.ID, "expected_version": sc.Version, "name": "Weekly review",
		"instruction": "Review my week", "time_of_day": "09:00", "time_zone": "UTC", "recurrence": map[string]any{"freq": "weekly"}, "enabled": false})
	sync()
	if _, ok = f.armed["schedule-"+sc.ID]; ok {
		t.Fatal("a disabled schedule must have no wake")
	}
}

func TestRoutineWakeIsTheNextReminderTime(t *testing.T) {
	s, f, sync := newSynced(t)
	at := time.Now().UTC().Add(3 * time.Hour).Format("15:04")
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": "r1", "title": "Stretch", "nudges": []map[string]any{{"at": at}}, "time_zone": "UTC"})
	sync()
	w, ok := f.armed["routine-"+r.ID]
	if !ok || w.At.UTC().Format("15:04") != at || !w.At.After(time.Now()) || w.Kind != "nudge" {
		t.Fatalf("routine wake: %+v", f.armed)
	}
	execute[engine.Routine](t, s, "routines_delete", map[string]any{"idempotency_key": "r2", "id": r.ID, "expected_version": r.Version})
	sync()
	if _, ok = f.armed["routine-"+r.ID]; ok {
		t.Fatal("a deleted routine must have no wake")
	}
}

func TestAWakeDeletedBehindTheEnginesBackIsArmedAgain(t *testing.T) {
	s, f, sync := newSynced(t)
	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": "d1", "title": "Pay rent", "due_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)})
	sync()
	name := "reminder-" + r.ID
	delete(f.armed, name) // someone deleted the Schedule in Temporal
	sync()
	if _, ok := f.armed[name]; !ok {
		t.Fatal("the missing wake was not armed again")
	}
}

// executeIn runs an operation as the agent would through MCP: with the calling session on the context.
func executeIn[T any](t *testing.T, s *engine.Service, session, name string, body any) T {
	t.Helper()
	raw, _ := json.Marshal(body)
	response, e := s.Execute(engine.WithSession(context.Background(), session), name, raw)
	if e != nil {
		t.Fatalf("%s: %v", name, engine.PublicError(e))
	}
	var v T
	if e = json.Unmarshal(response, &v); e != nil {
		t.Fatal(e)
	}
	return v
}

func TestAWakeLandsOnTheSessionItWasCreatedFrom(t *testing.T) {
	s, f, sync := newSynced(t)
	web := "agent:main:web:user:u9:session:abc"
	due := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)

	r := executeIn[engine.Reminder](t, s, web, "reminders_create", map[string]any{"idempotency_key": "ss1", "title": "From the web chat", "due_at": due})
	sc := executeIn[engine.Schedule](t, s, web, "schedules_create", map[string]any{"idempotency_key": "ss2", "name": "Review", "instruction": "Review", "time_of_day": "09:00", "recurrence": map[string]any{"freq": "daily"}})
	ro := executeIn[engine.Routine](t, s, web, "routines_create", map[string]any{"idempotency_key": "ss3", "title": "Stretch", "nudges": []map[string]any{{"at": time.Now().UTC().Add(3 * time.Hour).Format("15:04")}}})
	app := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": "ss4", "title": "From the app", "due_at": due})
	sync()

	// The branch suffix is stripped, as arm_wake does, so the wake goes to the user's canonical session.
	for _, name := range []string{"reminder-" + r.ID, "schedule-" + sc.ID, "routine-" + ro.ID} {
		if got := f.armed[name].SessionKey; got != "agent:main:web:user:u9" {
			t.Errorf("%s armed on %q", name, got)
		}
	}
	// No session (the app calls over HTTP): the waker's default.
	if got := f.armed["reminder-"+app.ID].SessionKey; got != "" {
		t.Errorf("app-created reminder armed on %q, want the default", got)
	}
	// Editing it later does not move it off the session that created it.
	execute[engine.Reminder](t, s, "reminders_update", map[string]any{"idempotency_key": "ss5", "id": r.ID, "expected_version": r.Version, "title": "Renamed", "due_at": due})
	n := len(f.log)
	sync()
	if got := f.armed["reminder-"+r.ID].SessionKey; got != "agent:main:web:user:u9" {
		t.Errorf("edit moved the wake to %q (log %v)", got, f.log[n:])
	}
}

func TestMovingAReminderToAnotherSessionTakesItOffTheOld(t *testing.T) {
	s, f, sync := newSynced(t)
	due := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	r := executeIn[engine.Reminder](t, s, "agent:main:ios:user:u1", "reminders_create", map[string]any{"idempotency_key": "mv1", "title": "Move me", "due_at": due})
	sync()
	execute[engine.Reminder](t, s, "reminders_update", map[string]any{"idempotency_key": "mv2", "id": r.ID, "expected_version": r.Version, "title": "Move me", "due_at": due, "session_id": "agent:main:web:user:u1"})
	sync()
	if got := f.armed["reminder-"+r.ID].SessionKey; got != "agent:main:web:user:u1" {
		t.Fatalf("armed on %q", got)
	}
	if !slices.Contains(f.log, "cancel agent:main:ios:user:u1/reminder-"+r.ID) {
		t.Fatalf("the old session's wake was not cancelled: %v", f.log)
	}
}

func TestGoalCheckInIsAScheduleLinkedToTheGoal(t *testing.T) {
	s, f, sync := newSynced(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": "g1", "title": "Run a 10k", "weekly_target": 3})
	sc := execute[engine.Schedule](t, s, "schedules_create", map[string]any{"idempotency_key": "g2", "name": "Run check-in", "instruction": "How is the running going?",
		"time_of_day": "18:00", "time_zone": "UTC", "recurrence": map[string]any{"freq": "weekly", "weekdays": []int{0}}, "link": map[string]any{"entity": "goal", "id": g.ID}})
	if sc.Link == nil || sc.Link.ID != g.ID {
		t.Fatalf("link not stored: %+v", sc.Link)
	}
	sync()
	w, ok := f.armed["schedule-"+sc.ID]
	if !ok || w.Kind != "goal_checkin" || w.Subject != "goal:"+g.ID || !strings.Contains(w.Objective, "Run a 10k") || !strings.Contains(w.Objective, "goals_get") {
		t.Fatalf("goal check-in wake: %+v", w)
	}
	errorCode(t, s, "schedules_create", map[string]any{"idempotency_key": "g3", "name": "Bad link", "instruction": "x", "time_of_day": "09:00",
		"recurrence": map[string]any{"freq": "daily"}, "link": map[string]any{"entity": "banana", "id": g.ID}}, "invalid_input")
}

func TestMonitorWakeAndRecordedChecks(t *testing.T) {
	s, f, sync, pool := newSyncedPool(t)
	m := execute[engine.Monitor](t, s, "monitors_create", map[string]any{"idempotency_key": "m1", "name": "Flight prices", "objective": "Tell me if London to Lisbon drops under 80", "every_minutes": 60})
	if !m.Enabled || m.LastCheckedAt != nil || !m.NextAt.After(time.Now().Add(59*time.Minute)) {
		t.Fatalf("created: %+v", m)
	}
	errorCode(t, s, "monitors_create", map[string]any{"idempotency_key": "m2", "name": "flight PRICES", "objective": "x", "every_minutes": 60}, "conflict")
	errorCode(t, s, "monitors_create", map[string]any{"idempotency_key": "m3", "name": "Too often", "objective": "x", "every_minutes": 5}, "invalid_input")

	sync()
	w, ok := f.armed["monitor-"+m.ID]
	if !ok || w.Kind != "monitor" || w.Subject != "monitor:"+m.ID || !w.At.Equal(m.NextAt) || !strings.Contains(w.Objective, "monitors_record") {
		t.Fatalf("monitor wake: %+v", f.armed)
	}

	// The agent records each check; that is not an edit by the person, so the version holds.
	rec := execute[engine.Monitor](t, s, "monitors_record", map[string]any{"idempotency_key": "m4", "id": m.ID, "result": "Cheapest is 112, no change", "triggered": false})
	if rec.LastCheckedAt == nil || rec.LastResult != "Cheapest is 112, no change" || rec.LastTriggeredAt != nil || rec.Version != m.Version {
		t.Fatalf("recorded: %+v", rec)
	}
	rec = execute[engine.Monitor](t, s, "monitors_record", map[string]any{"idempotency_key": "m5", "id": m.ID, "result": "Dropped to 74", "triggered": true})
	if rec.LastTriggeredAt == nil {
		t.Fatalf("a triggered check must be remembered: %+v", rec)
	}

	// An hour late and then some: missed checks are skipped, the next slot is the first one still ahead.
	if _, e := pool.Exec(context.Background(), "UPDATE life.monitors SET next_at = now() - interval '150 minutes' WHERE id=$1", m.ID); e != nil {
		t.Fatal(e)
	}
	sync()
	after := execute[engine.Monitor](t, s, "monitors_get", map[string]any{"id": m.ID})
	if !after.NextAt.After(time.Now()) || after.NextAt.After(time.Now().Add(time.Hour)) || !f.armed["monitor-"+m.ID].At.Equal(after.NextAt) {
		t.Fatalf("not moved on to the next slot: next %v armed %v", after.NextAt, f.armed["monitor-"+m.ID].At)
	}

	upd := execute[engine.Monitor](t, s, "monitors_update", map[string]any{"idempotency_key": "m6", "id": m.ID, "expected_version": after.Version, "name": "Flight prices",
		"objective": "Tell me if London to Lisbon drops under 80", "every_minutes": 60, "enabled": false})
	sync()
	if _, armed := f.armed["monitor-"+m.ID]; upd.Enabled || armed {
		t.Fatalf("a paused monitor must have no wake: %+v", upd)
	}
}

func TestARepeatingReminderMovesItsDueTimeOnAfterItFires(t *testing.T) {
	s, f, sync, pool := newSyncedPool(t)
	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": "rp1", "title": "Drink water", "time_zone": "UTC",
		"due_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "recurrence": map[string]any{"freq": "daily"}})
	sync()
	// It fired a while ago and nobody completed it: the Schedule has done its job and the reminder must show the next one.
	if _, e := pool.Exec(context.Background(), "UPDATE life.reminders SET due_at = now() - interval '10 minutes' WHERE id=$1", r.ID); e != nil {
		t.Fatal(e)
	}
	sync()
	got := execute[engine.Reminder](t, s, "reminders_get", map[string]any{"id": r.ID})
	if got.DueAt == nil || !got.DueAt.After(time.Now()) || got.DueAt.After(time.Now().Add(25*time.Hour)) {
		t.Fatalf("due time not moved to the next occurrence: %v", got.DueAt)
	}
	if got.Version != r.Version {
		t.Fatalf("moving the due time on is bookkeeping, not an edit: version %d -> %d", r.Version, got.Version)
	}
	if !f.armed["reminder-"+r.ID].At.Equal(*got.DueAt) {
		t.Fatalf("armed for %v, due %v", f.armed["reminder-"+r.ID].At, got.DueAt)
	}
	// A one-off never moves.
	one := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": "rp2", "title": "Once", "due_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	sync()
	if g := execute[engine.Reminder](t, s, "reminders_get", map[string]any{"id": one.ID}); !g.DueAt.Equal(*one.DueAt) {
		t.Fatalf("a one-off moved: %v", g.DueAt)
	}
}
