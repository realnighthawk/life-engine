package engine_test

import (
	"context"
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
func (f *fakeWaker) Cancel(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.armed, name)
	f.log = append(f.log, "cancel "+name)
	return nil
}

func (f *fakeWaker) Exists(_ context.Context, name string) (bool, error) {
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
