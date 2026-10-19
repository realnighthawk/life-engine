package engine_test

import (
	"testing"

	"github.com/realnighthawk/life-engine/internal/engine"
)

// Deleting a goal takes what exists only because of it, and leaves what is shared or merely mentions it.
func TestDeletingAGoalRemovesWhatExistsOnlyBecauseOfIt(t *testing.T) {
	s := integrationService(t)
	goal := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K", "steps": []map[string]any{{"title": "Run 1 km"}, {"title": "Run 3 km"}}})
	other := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Get fit"})
	own := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Morning run", "minutes": 30})
	shared := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Stretch", "minutes": 10})
	for _, l := range []struct{ goal, routine string }{{goal.ID, own.ID}, {goal.ID, shared.ID}, {other.ID, shared.ID}} {
		execute[engine.Goal](t, s, "goals_link_routine", map[string]any{"idempotency_key": key(), "goal_id": l.goal, "routine_id": l.routine})
	}
	execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Buy shoes", "link": map[string]any{"entity": "goal", "id": goal.ID}})
	loose := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Call mum"})
	execute[engine.Note](t, s, "notes_create", map[string]any{"idempotency_key": key(), "title": "Plan", "body": "x", "goal_id": goal.ID})
	execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "Ran today", "goal_ids": []string{goal.ID}})
	both := execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "Ran and read", "goal_ids": []string{goal.ID, other.ID}})
	execute[engine.Commitment](t, s, "commitments_add", map[string]any{"idempotency_key": key(), "week_start": "2026-10-12", "step_id": goal.Steps[0].ID})
	execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Run 1 km", "minutes": 25, "step_id": goal.Steps[0].ID})

	got := execute[engine.GoalDeletion](t, s, "goals_delete", map[string]any{"idempotency_key": key(), "id": goal.ID, "expected_version": goal.Version})
	want := map[string]int{"routines": 1, "reminders": 1, "notes": 1, "journal_entries": 1, "commitments": 1, "plan_items": 1, "steps": 2}
	for kind, n := range want {
		if got.Removed[kind] != n {
			t.Errorf("%s: removed %d, want %d (%v)", kind, got.Removed[kind], n, got.Removed)
		}
	}
	if got.Goal.ArchivedAt == nil {
		t.Error("the goal itself is gone")
	}

	routines := execute[engine.Page[engine.Routine]](t, s, "routines_list", map[string]any{})
	if len(routines.Items) != 1 || routines.Items[0].ID != shared.ID {
		t.Errorf("only the shared routine should remain: %+v", routines.Items)
	}
	if r := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{}); len(r.Items) != 1 || r.Items[0].ID != loose.ID {
		t.Errorf("only the unlinked reminder should remain: %+v", r.Items)
	}
	j := execute[engine.Page[engine.JournalEntry]](t, s, "journal_list", map[string]any{})
	if len(j.Items) != 1 || j.Items[0].ID != both.ID || len(j.Items[0].GoalIDs) != 1 || j.Items[0].GoalIDs[0] != other.ID {
		t.Errorf("the journal entry about two goals stays, about the other one: %+v", j.Items)
	}
	if kept := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": other.ID}); len(kept.RoutineIDs) != 1 || kept.RoutineIDs[0] != shared.ID {
		t.Errorf("the other goal keeps the shared routine: %+v", kept.RoutineIDs)
	}
}
