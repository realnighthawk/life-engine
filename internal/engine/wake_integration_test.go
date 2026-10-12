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

func TestReminderWakeFollowsTheRow(t *testing.T) {
	s, _ := integrationEnv(t)
	f := &fakeWaker{armed: map[string]engine.Wake{}}
	s.SetWaker(context.Background(), f, time.Hour) // the loop's first pass is racy with ours; SyncWakes is idempotent
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
