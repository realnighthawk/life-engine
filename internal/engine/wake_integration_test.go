package engine_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

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
	s, _ := integrationEnv(t)
	f := &fakeWaker{armed: map[string]engine.Wake{}}
	s.UseWaker(f)
	return s, f, func() {
		t.Helper()
		if e := s.SyncWakes(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
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
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": "r1", "title": "Stretch", "remind_at": at, "time_zone": "UTC"})
	sync()
	w, ok := f.armed["routine-"+r.ID]
	if !ok || w.At.UTC().Format("15:04") != at || !w.At.After(time.Now()) || w.Kind != "routine" {
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
	ro := executeIn[engine.Routine](t, s, web, "routines_create", map[string]any{"idempotency_key": "ss3", "title": "Stretch", "remind_at": time.Now().UTC().Add(3 * time.Hour).Format("15:04")})
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
