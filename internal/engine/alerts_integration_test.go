package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func pendingNotices(t *testing.T, s *engine.Service) []engine.Event {
	t.Helper()
	var out []engine.Event
	for _, e := range execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items {
		if e.EventKey == "life:notice" {
			out = append(out, e)
		}
	}
	return out
}

func TestAPersonsAlertIsHandedBackForTheGatewayAndRecordsNothingHere(t *testing.T) {
	s := integrationService(t)
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	task := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Call Sam", "notes": "about the car", "at": at.Format(time.RFC3339), "alert_before_minutes": 10, "is_priority": true})
	fired := at.Add(-10 * time.Minute)
	var first engine.FireResult
	for i := 0; i < 2; i++ { // a retried firing names the same occurrence
		res, err := s.Fire(context.Background(), task.ID, fired)
		if err != nil || !res.Fired || res.Wake != nil || res.Notify == nil {
			t.Fatalf("fire: %+v %v", res, err)
		}
		if i == 0 {
			first = res
		} else if *res.Notify != *first.Notify {
			t.Fatalf("the same occurrence must give the same event: %+v vs %+v", res.Notify, first.Notify)
		}
	}
	n := first.Notify
	if n.Title != "Call Sam" || n.Priority != "high" || n.LinkEntity != "task" || n.LinkID != task.ID || n.EventID == "" || !strings.Contains(n.Body, "about the car") {
		t.Fatalf("notice: %+v", n)
	}
	// Delivery is the workflow's job: nothing is recorded for mcp-hub to collect.
	if got := pendingNotices(t, s); len(got) != 0 {
		t.Fatalf("a person's alert must not go through the hub's feed: %+v", got)
	}
}

func TestAnAlertThatNoLongerMattersFiresNothing(t *testing.T) {
	s := integrationService(t)
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	mk := func(title string) engine.Item {
		return execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": title, "at": at.Format(time.RFC3339), "alert_before_minutes": 0})
	}
	done, gone, cleared := mk("done"), mk("gone"), mk("cleared")
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": done.ID, "expected_version": 1})
	execute[engine.Item](t, s, "tasks_delete", map[string]any{"idempotency_key": key(), "id": gone.ID, "expected_version": 1})
	rep := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "rep", "at": at.Format(time.RFC3339), "alert_before_minutes": 0, "recurrence": map[string]any{"freq": "daily"}})
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": rep.ID, "expected_version": 1, "at": at.Format(time.RFC3339)})
	_ = cleared
	for name, id := range map[string]string{"done": done.ID, "deleted": gone.ID, "an occurrence already cleared": rep.ID, "missing": "99999999-9999-4999-8999-999999999999"} {
		res, err := s.Fire(context.Background(), id, at)
		if err != nil || res.Fired || res.Skip == "" {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	if got := pendingNotices(t, s); len(got) != 0 {
		t.Fatalf("notices: %+v", got)
	}
}

func TestAnAgentsAlertNamesTheConversationToWake(t *testing.T) {
	s := integrationService(t)
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	ctx := engine.WithSession(context.Background(), "agent:main:ios:user:u1:session:s9")
	raw := []byte(`{"idempotency_key":"` + key() + `","title":"Check the deploy","notes":"look at the error rate","subject":"agent","at":"` + at.Format(time.RFC3339) + `","alert_before_minutes":0}`)
	out, err := s.Execute(ctx, "tasks_create", raw)
	if err != nil {
		t.Fatal(engine.PublicError(err))
	}
	var task engine.Item
	mustUnmarshal(t, out, &task)
	res, err := s.Fire(context.Background(), task.ID, at)
	if err != nil || !res.Fired || res.Wake == nil {
		t.Fatalf("fire: %+v %v", res, err)
	}
	w := res.Wake
	if w.SessionKey != "agent:main:ios:user:u1" || w.Objective != "Check the deploy\n\nlook at the error rate" || w.WakeID == "" {
		t.Fatalf("wake: %+v", w)
	}
	if got := pendingNotices(t, s); len(got) != 0 {
		t.Fatalf("an agent's alert is not a notice to the person: %+v", got)
	}
}

type fakeWaker struct {
	mu      sync.Mutex
	applied map[string]engine.AlertSpec
	log     []string
	wakes   map[string]engine.FireWake // by workflow id: starting one twice is one wake
	failing bool
}

func newFakeWaker() *fakeWaker {
	return &fakeWaker{applied: map[string]engine.AlertSpec{}, wakes: map[string]engine.FireWake{}}
}
func (f *fakeWaker) Wake(_ context.Context, id string, w engine.FireWake) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing {
		return errors.New("temporal is down")
	}
	f.wakes[id] = w
	return nil
}
func (f *fakeWaker) Apply(_ context.Context, id string, sp engine.AlertSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied[id+"/"+sp.Name] = sp
	f.log = append(f.log, "apply "+sp.Name)
	return nil
}
func (f *fakeWaker) Remove(_ context.Context, id, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.applied[id+"/"+name]; ok {
		delete(f.applied, id+"/"+name)
		f.log = append(f.log, "remove "+name)
	}
	return nil
}
func (f *fakeWaker) Exists(_ context.Context, id, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.applied[id+"/"+name]
	return ok, nil
}

func TestTheEngineKeepsItsSchedulesMatchedToTheRows(t *testing.T) {
	s := integrationService(t)
	w := newFakeWaker()
	s.UseWaker(w)
	ctx := context.Background()
	at := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	task := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "x", "at": at.Format(time.RFC3339), "alert_before_minutes": 30})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "no alert", "at": at.Format(time.RFC3339)})

	if err := s.SyncAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	sp, ok := w.applied[task.ID+"/main"]
	if !ok || sp.Once == nil || !sp.Once.Equal(at.Add(-30*time.Minute)) || len(w.applied) != 1 {
		t.Fatalf("armed: %+v", w.applied)
	}
	// Nothing changed: nothing is re-armed.
	before := len(w.log)
	if err := s.SyncAlerts(ctx); err != nil || len(w.log) != before {
		t.Fatalf("an unchanged item was re-armed: %v %v", w.log, err)
	}
	// A Schedule deleted behind the engine's back is armed again.
	delete(w.applied, task.ID+"/main")
	if err := s.SyncAlerts(ctx); err != nil || len(w.applied) != 1 {
		t.Fatalf("not re-armed: %+v %v", w.applied, err)
	}
	// A change updates it in place; a snooze on a one-off moves it, not adds one.
	execute[engine.Item](t, s, "tasks_snooze", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "until": at.Add(time.Hour).Format(time.RFC3339)})
	if err := s.SyncAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.applied[task.ID+"/main"]; got.Once == nil || !got.Once.Equal(at.Add(time.Hour)) || len(w.applied) != 1 {
		t.Fatalf("snoozed: %+v", w.applied)
	}
	// Completing, or deleting, takes the Schedule away.
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 2})
	if err := s.SyncAlerts(ctx); err != nil || len(w.applied) != 0 {
		t.Fatalf("done task still armed: %+v %v", w.applied, err)
	}
}
