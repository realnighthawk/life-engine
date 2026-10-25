package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

const branch = "agent:main:web:user:u1:session:s9"
const scope = "agent:main:web:user:u1"

func inSession(t *testing.T, s *engine.Service, session, name string, body any) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(body)
	out, err := s.Execute(engine.WithSession(context.Background(), session), name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, engine.PublicError(err))
	}
	return out
}

func arm(t *testing.T, s *engine.Service, name, event, objective string) engine.Watch {
	t.Helper()
	var w engine.Watch
	if err := json.Unmarshal(inSession(t, s, branch, "watches_create", map[string]any{"idempotency_key": key(), "name": name, "event_key": event, "objective": objective}), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func deliver(t *testing.T, s *engine.Service, event, id string) engine.DeliverEventResult {
	t.Helper()
	return execute[engine.DeliverEventResult](t, s, "events_deliver", map[string]any{"event_key": event, "event_id": id, "detail": "the budget crossed its line", "source": "finance"})
}

func TestAWatchBelongsToTheUserStableConversationNotTheBranch(t *testing.T) {
	s := integrationService(t)
	w := arm(t, s, "budget-line", "finance:budget_alert", "Check the spend and tell the person.")
	if w.SessionKey != scope || w.Version != 1 {
		t.Fatalf("watch: %+v", w)
	}
	errorCode(t, s, "watches_create", map[string]any{"idempotency_key": key(), "name": "x", "event_key": "e", "objective": "o"}, "invalid_input") // no conversation
}

func TestArmingTheSameNameRevisesTheWatchInsteadOfAddingASecond(t *testing.T) {
	s := integrationService(t)
	first := arm(t, s, "budget-line", "finance:budget_alert", "one")
	second := arm(t, s, "budget-line", "finance:budget_overspent", "two")
	if second.ID != first.ID || second.Version != 2 || second.EventKey != "finance:budget_overspent" || second.Objective != "two" {
		t.Fatalf("revised: %+v", second)
	}
	page := execute[engine.Page[engine.Watch]](t, s, "watches_list", map[string]any{})
	if page.Total != 1 {
		t.Fatalf("list: %+v", page)
	}
	// The same request replayed under the same idempotency key is the same answer, not a third revision.
	k := key()
	for i := 0; i < 2; i++ {
		inSession(t, s, branch, "watches_create", map[string]any{"idempotency_key": k, "name": "other", "event_key": "e", "objective": "o"})
	}
	if got := execute[engine.Page[engine.Watch]](t, s, "watches_list", map[string]any{}); got.Total != 2 {
		t.Fatalf("list after replay: %+v", got)
	}
}

func TestAnEventQueuesOneWakePerWatchAndRedeliveryQueuesNothingMore(t *testing.T) {
	s := integrationService(t)
	arm(t, s, "a", "finance:budget_alert", "first objective")
	arm(t, s, "b", "finance:budget_alert", "second objective")
	arm(t, s, "c", "maps:arrival", "elsewhere")
	got := deliver(t, s, "finance:budget_alert", "ev-1")
	if got.Matched != 2 || got.Queued != 2 {
		t.Fatalf("first delivery: %+v", got)
	}
	again := deliver(t, s, "finance:budget_alert", "ev-1")
	if again.Matched != 2 || again.Queued != 0 {
		t.Fatalf("a re-delivery must queue nothing: %+v", again)
	}
	if none := deliver(t, s, "nobody:listens", "ev-2"); none.Matched != 0 {
		t.Fatalf("no watch: %+v", none)
	}
}

func TestACancelledOrExpiredWatchDoesNotFire(t *testing.T) {
	s := integrationService(t)
	gone := arm(t, s, "gone", "finance:budget_alert", "o")
	execute[engine.Watch](t, s, "watches_delete", map[string]any{"idempotency_key": key(), "id": gone.ID, "expected_version": 1})
	inSession(t, s, branch, "watches_create", map[string]any{"idempotency_key": key(), "name": "old", "event_key": "finance:budget_alert", "objective": "o", "expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	live := arm(t, s, "live", "finance:budget_alert", "o")
	if got := deliver(t, s, "finance:budget_alert", "ev-1"); got.Matched != 1 || got.Queued != 1 {
		t.Fatalf("only the live watch: %+v", got)
	}
	_ = live
}

func TestUpdateKeepsWhatItIsNotTold(t *testing.T) {
	s := integrationService(t)
	w := arm(t, s, "w", "finance:budget_alert", "original")
	up := execute[engine.Watch](t, s, "watches_update", map[string]any{"idempotency_key": key(), "id": w.ID, "expected_version": 1, "objective": "reworded"})
	if up.Objective != "reworded" || up.EventKey != "finance:budget_alert" || up.Version != 2 {
		t.Fatalf("update: %+v", up)
	}
	errorCode(t, s, "watches_update", map[string]any{"idempotency_key": key(), "id": w.ID, "expected_version": 1, "objective": "stale"}, "conflict")
}

func TestTheWakeIsSentFromTheFiringAndRetriedUntilItIs(t *testing.T) {
	s := integrationService(t)
	f := newFakeWaker()
	s.UseWaker(f)
	arm(t, s, "budget-line", "finance:budget_alert", "Check the spend and tell the person.")
	deliver(t, s, "finance:budget_alert", "ev-1")

	f.failing = true
	if err := s.SendWatchWakes(context.Background()); err == nil {
		t.Fatal("a wake that cannot be started must be reported")
	}
	if len(f.wakes) != 0 {
		t.Fatalf("wakes: %+v", f.wakes)
	}
	f.failing = false
	if err := s.SendWatchWakes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.wakes) != 1 {
		t.Fatalf("wakes: %+v", f.wakes)
	}
	for id, w := range f.wakes {
		if w.SessionKey != scope || w.Objective != "Check the spend and tell the person." || w.Why != "the budget crossed its line" || w.WakeID != "watch:budget-line:ev-1" || id == "" {
			t.Fatalf("wake: %s %+v", id, w)
		}
	}
	// Sent once: a later pass finds nothing left.
	f.wakes = map[string]engine.FireWake{}
	if err := s.SendWatchWakes(context.Background()); err != nil || len(f.wakes) != 0 {
		t.Fatalf("second pass: %v %+v", err, f.wakes)
	}
}
