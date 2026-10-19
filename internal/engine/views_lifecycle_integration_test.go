package engine_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

func goalWithView(t *testing.T, s *engine.Service) (engine.Goal, engine.View) {
	t.Helper()
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Lose 5 kg"})
	in := weightView(g.ID)
	in["idempotency_key"] = key()
	return g, execute[engine.View](t, s, "views_create", in)
}

func TestViewUpdateAndDelete(t *testing.T) {
	s := integrationService(t)
	_, v := goalWithView(t, s)
	blocks := []map[string]any{{"id": "steps", "kind": "steps", "title": "Steps", "source": "goal_steps"}}
	up := execute[engine.View](t, s, "views_update", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": v.Version,
		"metrics": []map[string]any{}, "blocks": blocks})
	if up.Version != 2 || len(up.Blocks) != 1 || len(up.Metrics) != 0 || up.CatalogID != v.CatalogID || up.SubjectID != v.SubjectID {
		t.Fatalf("updated: %+v", up)
	}
	errorCode(t, s, "views_update", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": v.Version, "metrics": []map[string]any{}, "blocks": blocks}, "conflict")
	errorCode(t, s, "views_update", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": up.Version, "metrics": []map[string]any{},
		"blocks": []map[string]any{{"id": "x", "type": "html", "title": "x"}}}, "invalid_input")
	execute[engine.View](t, s, "views_delete", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": up.Version})
	errorCode(t, s, "views_get", map[string]any{"id": v.ID}, "not_found")
	errorCode(t, s, "views_update", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": up.Version + 1, "metrics": []map[string]any{}, "blocks": blocks}, "conflict")
	if list := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": v.SubjectID}); len(list.Items) != 0 {
		t.Fatalf("a removed view is not listed: %+v", list)
	}
}

func TestMeasurementsAgainstAViewsMetrics(t *testing.T) {
	s := integrationService(t)
	g, v := goalWithView(t, s)
	// No goals.tracker and no expected_tracker_revision: the view defines the metric.
	m := execute[engine.GoalMeasurement](t, s, "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "waist", "value": 90})
	if m.Unit != "cm" || m.Value != 90 {
		t.Fatalf("recorded: %+v", m)
	}
	errorCode(t, s, "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "ghost", "value": 1}, "invalid_input")
	up := execute[engine.GoalMeasurement](t, s, "goal_measurements_update", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": m.Version, "value": 89})
	if up.Value != 89 {
		t.Fatalf("updated: %+v", up)
	}
	execute[engine.GoalMeasurement](t, s, "goal_measurements_delete", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": up.Version})

	// History protection: the recorded metric stays, in its unit.
	blocks := []map[string]any{{"id": "steps", "kind": "steps", "title": "Steps", "source": "goal_steps"}}
	errorCode(t, s, "views_update", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": v.Version, "metrics": []map[string]any{}, "blocks": blocks}, "conflict")
	errorCode(t, s, "views_update", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": v.Version, "blocks": blocks,
		"metrics": []map[string]any{{"id": "waist", "label": "Waist", "unit": "in"}}}, "conflict")
}

func TestBatchUndo(t *testing.T) {
	s := integrationService(t)
	routine, goal := uuid.NewString(), uuid.NewString()
	steps := append(plan(routine, goal), op("views_create", weightView(goal)))
	k := key()
	execute[engine.BatchResult](t, s, "batch_apply", map[string]any{"idempotency_key": k, "operations": steps})
	if g, r, m := counts(t, s); g != 1 || r != 1 || m != 1 {
		t.Fatalf("before: %d %d %d", g, r, m)
	}
	res := execute[engine.BatchUndoResult](t, s, "batch_undo", map[string]any{"idempotency_key": key(), "batch_key": k})
	if len(res.Steps) != 4 || res.Steps[0].Operation != "views_create" || res.Steps[3].Operation != "routines_create" {
		t.Fatalf("undo runs newest first: %+v", res.Steps)
	}
	for _, st := range res.Steps {
		if st.Status != "removed" {
			t.Fatalf("every record should be removed: %+v", res.Steps)
		}
	}
	if g, r, m := counts(t, s); g+r+m != 0 {
		t.Fatalf("undo left records: %d %d %d", g, r, m)
	}
	if list := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": goal}); len(list.Items) != 0 {
		t.Fatalf("the view should be gone: %+v", list)
	}
	again := execute[engine.BatchUndoResult](t, s, "batch_undo", map[string]any{"idempotency_key": key(), "batch_key": k})
	for _, st := range again.Steps {
		if st.Status != "already_removed" {
			t.Fatalf("a second undo reports what is already gone: %+v", again.Steps)
		}
	}
	errorCode(t, s, "batch_undo", map[string]any{"idempotency_key": key(), "batch_key": "never-used"}, "not_found")
}

func TestBatchUndoLeavesAnExistingLinkAlone(t *testing.T) {
	s := integrationService(t)
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Stretch"})
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Be supple"})
	k := key()
	execute[engine.BatchResult](t, s, "batch_apply", map[string]any{"idempotency_key": k, "operations": []map[string]any{
		op("goals_link_routine", map[string]any{"goal_id": g.ID, "routine_id": r.ID})}})
	res := execute[engine.BatchUndoResult](t, s, "batch_undo", map[string]any{"idempotency_key": key(), "batch_key": k})
	if len(res.Steps) != 1 || res.Steps[0].Status != "not_undone" {
		t.Fatalf("a link between existing records is reported, not silently kept: %+v", res.Steps)
	}
}
