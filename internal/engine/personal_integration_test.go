package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestIntentionLinksAndProgress(t *testing.T) {
	s := integrationService(t)
	it := execute[engine.Intention](t, s, "intentions_create", map[string]any{
		"idempotency_key": key(), "title": "Run a 10k", "kind": "achieve",
		"target": map[string]any{"value": 10, "unit": "km"},
	})
	if it.Kind != "achieve" || it.Status != "active" || it.Origin != "person" || it.Target == nil || it.Target.Unit != "km" {
		t.Fatalf("created: %+v", it)
	}
	task := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Easy 5k"})
	execute[engine.RecordLink](t, s, "links_set", map[string]any{
		"idempotency_key": key(), "from_kind": "task", "from_id": task.ID,
		"relation": "serves", "to_kind": "intention", "to_id": it.ID,
	})
	got := execute[engine.Intention](t, s, "intentions_get", map[string]any{"id": it.ID})
	if got.Progress.Total != 1 || got.Progress.Done != 0 {
		t.Fatalf("progress: %+v", got.Progress)
	}
	links := execute[engine.Page[engine.RecordLink]](t, s, "links_list", map[string]any{"kind": "intention", "id": it.ID})
	if len(links.Items) != 1 || links.Items[0].Relation != "serves" {
		t.Fatalf("links: %+v", links.Items)
	}
}

func TestCommitmentCompletionRecordsAnActivity(t *testing.T) {
	s := integrationService(t)
	c := execute[engine.Commitment](t, s, "commitments_create", map[string]any{
		"idempotency_key": key(), "title": "Pay rent", "due_on": "2026-11-01", "counterparty": "landlord",
	})
	if c.Status != "open" || c.Counterparty != "landlord" || c.DueOn == nil || *c.DueOn != "2026-11-01" {
		t.Fatalf("created: %+v", c)
	}
	met := execute[engine.Commitment](t, s, "commitments_complete", map[string]any{
		"idempotency_key": key(), "id": c.ID, "expected_version": c.Version, "note": "paid by transfer",
	})
	if met.Status != "met" {
		t.Fatalf("status: %s", met.Status)
	}
	acts := execute[engine.Page[engine.Activity]](t, s, "activities_list", map[string]any{"kind": "occurrence"})
	if len(acts.Items) != 1 || !strings.Contains(acts.Items[0].Text, "Met: Pay rent") {
		t.Fatalf("activity: %+v", acts.Items)
	}
	links := execute[engine.Page[engine.RecordLink]](t, s, "links_list", map[string]any{
		"kind": "commitment", "id": c.ID, "relation": "fulfils", "direction": "to",
	})
	if len(links.Items) != 1 || links.Items[0].FromKind != "activity" {
		t.Fatalf("link: %+v", links.Items)
	}
}

func TestChangeDecisionPromotesOnlyWithHumanConfirmation(t *testing.T) {
	s := integrationService(t)
	d := execute[engine.Decision](t, s, "decisions_create", map[string]any{
		"idempotency_key": key(),
		"question":        "Track rent as a commitment?",
		"kind":            "change",
		"options": []map[string]any{
			{"id": "accept", "label": "Yes, track it"},
			{"id": "dismiss", "label": "No"},
		},
		"effect": map[string]any{"operations": []map[string]any{{
			"operation": "commitments_create",
			"input":     map[string]any{"title": "Pay rent", "due_on": "2026-11-01"},
		}}},
		"derived_from": map[string]any{"bank_id": "bank", "record_id": "candidate", "snapshot": map[string]any{"months": 3}},
	})
	if d.Status != "open" || d.Kind != "change" || len(d.Options) != 2 {
		t.Fatalf("created: %+v", d)
	}
	errorCode(t, s, "decisions_resolve", map[string]any{
		"idempotency_key": key(), "id": d.ID, "expected_version": d.Version, "option": "accept",
	}, "invalid_input")
	resolved := execute[engine.Decision](t, s, "decisions_resolve", map[string]any{
		"idempotency_key": key(), "id": d.ID, "expected_version": d.Version, "option": "accept",
		"rationale": "it is a real obligation", "confirmation": map[string]any{"source": "turn", "ref": "turn-1"},
	})
	if resolved.Status != "decided" || resolved.ChosenOption == nil || *resolved.ChosenOption != "accept" {
		t.Fatalf("resolved: %+v", resolved)
	}
	cs := execute[engine.Page[engine.Commitment]](t, s, "commitments_list", map[string]any{})
	if len(cs.Items) != 1 || cs.Items[0].Title != "Pay rent" {
		t.Fatalf("promoted records: %+v", cs.Items)
	}
}

func TestStateAndAttention(t *testing.T) {
	s := integrationService(t)
	st := execute[engine.State](t, s, "states_create", map[string]any{
		"idempotency_key": key(), "kind": "fact", "topic": "running", "predicate": "current distance", "value": "3 km",
	})
	if st.Status != "confirmed" || string(st.Value) != `"3 km"` {
		t.Fatalf("created: %+v", st)
	}
	retracted := execute[engine.State](t, s, "states_retract", map[string]any{
		"idempotency_key": key(), "id": st.ID, "expected_version": st.Version,
	})
	if retracted.Status != "retracted" {
		t.Fatalf("retracted: %s", retracted.Status)
	}
	execute[engine.Commitment](t, s, "commitments_create", map[string]any{
		"idempotency_key": key(), "title": "Pay rent", "due_at": ts(-24 * time.Hour),
	})
	execute[engine.Decision](t, s, "decisions_create", map[string]any{
		"idempotency_key": key(), "question": "Gym or outdoors?",
		"options": []map[string]any{{"id": "gym", "label": "Gym"}, {"id": "out", "label": "Outdoors"}},
	})
	a := execute[engine.Attention](t, s, "attention_get", map[string]any{})
	if len(a.Commitments) != 1 || len(a.Decisions) != 1 {
		t.Fatalf("attention: %+v", a)
	}
}

func TestActivitiesAreCorrectable(t *testing.T) {
	s := integrationService(t)
	a := execute[engine.Activity](t, s, "activities_record", map[string]any{
		"idempotency_key": key(), "actor": "person", "kind": "action",
		"text": "I called the dentist", "occurred_at": ts(-time.Hour),
	})
	if a.Kind != "action" || a.Actor != "person" {
		t.Fatalf("recorded: %+v", a)
	}
	corrected := execute[engine.Activity](t, s, "activities_correct", map[string]any{
		"idempotency_key": key(), "id": a.ID, "text": "Called the dentist",
	})
	if corrected.Text != "Called the dentist" {
		t.Fatalf("corrected: %+v", corrected)
	}
}
