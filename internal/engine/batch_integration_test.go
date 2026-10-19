package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

func op(name string, input map[string]any) map[string]any {
	return map[string]any{"operation": name, "input": input}
}

// plan is a routine, a goal that uses it, and a reminder pointing at the goal: each refers to an id an earlier step created.
func plan(routine, goal string) []map[string]any {
	return []map[string]any{
		op("routines_create", map[string]any{"id": routine, "title": "Morning walk", "nudges": []map[string]any{{"at": "07:30"}}}),
		op("goals_create", map[string]any{"id": goal, "title": "Lose 5 kg", "routine_ids": []string{routine}, "steps": []map[string]any{{"title": "Weigh in"}}}),
		op("reminders_create", map[string]any{"title": "Weigh in", "due_at": ts(48 * 60 * 60 * 1e9), "link": map[string]any{"entity": "goal", "id": goal}}),
	}
}

func counts(t *testing.T, s *engine.Service) (goals, routines, reminders int) {
	t.Helper()
	g := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{})
	r := execute[engine.Page[engine.Routine]](t, s, "routines_list", map[string]any{})
	m := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{})
	return g.Total, r.Total, m.Total
}

func TestBatchDryRunCreatesNothing(t *testing.T) {
	s := integrationService(t)
	res := execute[engine.BatchResult](t, s, "batch_dry_run", map[string]any{"operations": plan(uuid.NewString(), uuid.NewString())})
	if !res.DryRun || len(res.Steps) != 3 {
		t.Fatalf("dry run: %+v", res)
	}
	if g, r, m := counts(t, s); g+r+m != 0 {
		t.Fatalf("a dry run left records behind: %d goals, %d routines, %d reminders", g, r, m)
	}
}

func TestBatchApplyCreatesEverythingLinked(t *testing.T) {
	s := integrationService(t)
	routine, goal := uuid.NewString(), uuid.NewString()
	res := execute[engine.BatchResult](t, s, "batch_apply", map[string]any{"idempotency_key": key(), "operations": plan(routine, goal)})
	if res.DryRun || len(res.Steps) != 3 {
		t.Fatalf("apply: %+v", res)
	}
	g := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": goal})
	if len(g.RoutineIDs) != 1 || g.RoutineIDs[0] != routine {
		t.Fatalf("goal should serve the routine created before it: %+v", g.RoutineIDs)
	}
	if goals, routines, reminders := counts(t, s); goals != 1 || routines != 1 || reminders != 1 {
		t.Fatalf("counts: %d %d %d", goals, routines, reminders)
	}
}

func TestBatchFailureLeavesNothing(t *testing.T) {
	s := integrationService(t)
	bad := plan(uuid.NewString(), uuid.NewString())
	bad[2] = op("reminders_create", map[string]any{"title": "Weigh in", "link": map[string]any{"entity": "goal", "id": uuid.NewString()}})
	// The first two steps succeed before the third fails, so this proves they are undone.
	for name, in := range map[string]map[string]any{
		"batch_apply":   {"idempotency_key": key(), "operations": bad},
		"batch_dry_run": {"operations": bad},
	} {
		raw, _ := json.Marshal(in)
		_, e := s.Execute(context.Background(), name, raw)
		if pe := engine.PublicError(e); e == nil || pe.Code != "invalid_input" || !strings.HasPrefix(pe.Message, "operations[2] reminders_create:") {
			t.Fatalf("%s: wanted invalid_input at operations[2], got %v", name, e)
		}
	}
	if g, r, m := counts(t, s); g+r+m != 0 {
		t.Fatalf("a failed batch left records behind: %d goals, %d routines, %d reminders", g, r, m)
	}
}

func TestBatchRetryIsIdempotent(t *testing.T) {
	s := integrationService(t)
	in := map[string]any{"idempotency_key": key(), "operations": plan(uuid.NewString(), uuid.NewString())}
	execute[engine.BatchResult](t, s, "batch_apply", in)
	execute[engine.BatchResult](t, s, "batch_apply", in)
	if g, r, m := counts(t, s); g != 1 || r != 1 || m != 1 {
		t.Fatalf("a retry duplicated records: %d %d %d", g, r, m)
	}
	in["operations"] = plan(uuid.NewString(), uuid.NewString())
	errorCode(t, s, "batch_apply", in, "conflict")
}

func TestBatchRejectsWhatItDoesNotCarry(t *testing.T) {
	s := integrationService(t)
	one := []map[string]any{op("goals_archive", map[string]any{"id": uuid.NewString(), "expected_version": 1})}
	errorCode(t, s, "batch_apply", map[string]any{"idempotency_key": key(), "operations": one}, "invalid_input")
	errorCode(t, s, "batch_apply", map[string]any{"idempotency_key": key(), "operations": []map[string]any{}}, "invalid_input")
}

func TestReminderLinkMustExist(t *testing.T) {
	s := integrationService(t)
	errorCode(t, s, "reminders_create", map[string]any{
		"idempotency_key": key(), "title": "Orphan", "link": map[string]any{"entity": "goal", "id": uuid.NewString()},
	}, "invalid_input")
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Real goal"})
	execute[engine.Reminder](t, s, "reminders_create", map[string]any{
		"idempotency_key": key(), "title": "Fine", "link": map[string]any{"entity": "goal", "id": g.ID},
	})
}
