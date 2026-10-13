package engine_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

func today() string { return time.Now().UTC().Format("2006-01-02") }

func TestGoalsAndSteps(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Learn Spanish", "why": "Trip in spring",
		"steps": []map[string]any{{"title": "Pick a course", "minutes": 10}, {"title": "First lesson"}}})
	if len(g.Steps) != 2 || g.Steps[1].Position != 2 || g.Steps[1].Minutes != 15 || g.WeeklyTarget != 3 || g.NextStep == nil || g.NextStep.Title != "Pick a course" {
		t.Fatalf("created: %+v", g)
	}
	st := execute[engine.Step](t, s, "steps_add", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "title": "Five minutes of vocab", "minutes": 5, "position": 1})
	if st.Position != 1 {
		t.Fatalf("inserted at %d", st.Position)
	}
	got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if got.Steps[0].ID != st.ID || got.Steps[1].Position != 2 || got.Steps[2].Position != 3 || got.Progress.Total != 3 {
		t.Fatalf("positions shift: %+v", got.Steps)
	}
	done := execute[engine.Step](t, s, "steps_complete", map[string]any{"idempotency_key": key(), "id": st.ID, "expected_version": 1, "done": true})
	if done.DoneAt == nil || done.Version != 2 {
		t.Fatalf("complete: %+v", done)
	}
	errorCode(t, s, "steps_complete", map[string]any{"idempotency_key": key(), "id": st.ID, "expected_version": 1, "done": false}, "conflict")
	got = execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if got.Progress.Done != 1 || got.NextStep.Title != "Pick a course" {
		t.Fatalf("progress: %+v", got.Progress)
	}
	// Reorder must list every step once.
	errorCode(t, s, "steps_reorder", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "step_ids": []string{got.Steps[0].ID}}, "invalid_input")
	re := execute[engine.Goal](t, s, "steps_reorder", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "step_ids": []string{got.Steps[2].ID, got.Steps[0].ID, got.Steps[1].ID}})
	if re.Steps[0].ID != got.Steps[2].ID || re.Steps[2].ID != got.Steps[1].ID {
		t.Fatalf("reordered: %+v", re.Steps)
	}
	// Deleting a step closes the gap and keeps its history.
	del := execute[engine.Step](t, s, "steps_delete", map[string]any{"idempotency_key": key(), "id": re.Steps[0].ID, "expected_version": 1})
	after := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if len(after.Steps) != 2 || after.Steps[0].Position != 1 || after.Steps[1].Position != 2 {
		t.Fatalf("after delete: %+v (deleted %s)", after.Steps, del.ID)
	}
	if h := execute[engine.Page[engine.Change]](t, s, "history_list", map[string]any{"entity_type": "step", "id": del.ID}); h.Total < 2 {
		t.Fatalf("a deleted step keeps its trail: %d", h.Total)
	}
	// Archive hides it from the default list and blocks edits.
	arch := execute[engine.Goal](t, s, "goals_archive", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": g.Version})
	if arch.ArchivedAt == nil {
		t.Fatal("not archived")
	}
	if l := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{}); l.Total != 0 {
		t.Fatalf("archived still listed: %d", l.Total)
	}
	errorCode(t, s, "steps_add", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "title": "x"}, "conflict")
	errorCode(t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "x", "weekly_target": 30}, "invalid_input")
}

func TestCommitments(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 10k", "steps": []map[string]any{{"title": "Run 2 km"}}})
	errorCode(t, s, "commitments_add", map[string]any{"idempotency_key": key(), "week_start": "2026-10-14", "title": "x"}, "invalid_input") // a Wednesday
	c := execute[engine.Commitment](t, s, "commitments_add", map[string]any{"idempotency_key": key(), "week_start": "2026-10-12", "step_id": g.Steps[0].ID})
	if c.Title != "Run 2 km" || c.GoalID != g.ID || c.WeekStart != "2026-10-12" {
		t.Fatalf("derived from the step: %+v", c)
	}
	week := execute[engine.CommitmentsWeek](t, s, "commitments_list", map[string]any{"week_start": "2026-10-12"})
	if week.Total != 1 || week.Done != 0 {
		t.Fatalf("week: %+v", week)
	}
	// Completing a commitment completes its step.
	execute[engine.Commitment](t, s, "commitments_complete", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 1, "done": true})
	if got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID}); got.Progress.Done != 1 {
		t.Fatalf("step follows: %+v", got.Progress)
	}
	execute[engine.Commitment](t, s, "commitments_delete", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 2})
	if w := execute[engine.CommitmentsWeek](t, s, "commitments_list", map[string]any{"week_start": "2026-10-12"}); w.Total != 0 {
		t.Fatalf("deleted: %+v", w)
	}
	errorCode(t, s, "commitments_add", map[string]any{"idempotency_key": key()}, "invalid_input") // nothing to commit to
}

func TestPlanItemsNextActionAndReplan(t *testing.T) {
	s := integrationService(t)
	d := today()
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Write", "steps": []map[string]any{{"title": "Outline", "minutes": 20}}})
	memo := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "day": d, "title": "Draft memo", "minutes": 40, "is_priority": true})
	sam := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Reply to Sam", "minutes": 10, "step_id": g.Steps[0].ID})
	execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Standup", "kind": "event", "minutes": 15})
	if sam.Day != d {
		t.Fatalf("day defaults to today in the person's zone: %s vs %s", sam.Day, d)
	}
	plan := execute[engine.PlanDay](t, s, "plan_get", map[string]any{})
	if len(plan.Items) != 3 || plan.Summary.Planned != 2 || plan.Summary.MinutesOpen != 50 || plan.Summary.MinutesEvents != 15 {
		t.Fatalf("plan: %+v", plan.Summary)
	}

	if na := execute[engine.NextActionResult](t, s, "next_action", map[string]any{"minutes": 60}); na.Suggestion == nil || na.Suggestion.Title != "Draft memo" {
		t.Fatalf("priority first: %+v", na.Suggestion)
	}
	if na := execute[engine.NextActionResult](t, s, "next_action", map[string]any{"minutes": 25}); na.Suggestion == nil || na.Suggestion.Source != "goal_step" {
		t.Fatalf("goal step when the priority is too long: %+v", na.Suggestion)
	}
	if na := execute[engine.NextActionResult](t, s, "next_action", map[string]any{"minutes": 5}); na.Suggestion != nil {
		t.Fatalf("nothing fits five minutes: %+v", na.Suggestion)
	}
	errorCode(t, s, "next_action", map[string]any{"minutes": 0}, "invalid_input")

	pre := execute[engine.Replan](t, s, "plan_replan_preview", map[string]any{"minutes_left": 30})
	if len(pre.MoveToTomorrow) != 1 || pre.MoveToTomorrow[0].ID != memo.ID {
		t.Fatalf("preview: %+v", pre)
	}

	// Completing a linked item completes its step; reopening reopens it.
	execute[engine.PlanItem](t, s, "plan_items_complete", map[string]any{"idempotency_key": key(), "id": sam.ID, "expected_version": 1, "done": true})
	if got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID}); got.Progress.Done != 1 {
		t.Fatalf("step follows the item: %+v", got.Progress)
	}
	execute[engine.PlanItem](t, s, "plan_items_complete", map[string]any{"idempotency_key": key(), "id": sam.ID, "expected_version": 2, "done": false})
	if got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID}); got.Progress.Done != 0 {
		t.Fatalf("reopened: %+v", got.Progress)
	}

	// Move clears the start time; delete/restore round trip; stale versions are refused.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	mv := execute[engine.PlanItem](t, s, "plan_items_move", map[string]any{"idempotency_key": key(), "id": memo.ID, "expected_version": 1, "day": tomorrow})
	if mv.Day != tomorrow {
		t.Fatalf("moved: %+v", mv)
	}
	errorCode(t, s, "plan_items_move", map[string]any{"idempotency_key": key(), "id": memo.ID, "expected_version": 1, "day": tomorrow}, "conflict")
	execute[engine.PlanItem](t, s, "plan_items_delete", map[string]any{"idempotency_key": key(), "id": sam.ID, "expected_version": 3})
	if p := execute[engine.PlanDay](t, s, "plan_get", map[string]any{}); len(p.Items) != 1 {
		t.Fatalf("after delete: %d", len(p.Items))
	}
}

func TestPlanProposals(t *testing.T) {
	s := integrationService(t)
	d := today()
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	a := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Draft memo", "minutes": 40, "is_priority": true})
	b := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Clean desk", "minutes": 20})
	errorCode(t, s, "plan_propose", map[string]any{"idempotency_key": key(), "day": d, "reason": "x", "changes": []map[string]any{{"action": "drop", "item_id": uuid.NewString()}}}, "invalid_input")
	errorCode(t, s, "plan_propose", map[string]any{"idempotency_key": key(), "day": d, "reason": "x", "changes": []map[string]any{}}, "invalid_input")
	p := execute[engine.PlanProposal](t, s, "plan_propose", map[string]any{"idempotency_key": key(), "day": d, "reason": "Your afternoon meeting ran long", "changes": []map[string]any{
		{"action": "move", "item_id": a.ID, "day": tomorrow}, {"action": "drop", "item_id": b.ID}, {"action": "add", "title": "Take a walk", "kind": "rest", "minutes": 15}}})
	if p.State != "pending" {
		t.Fatalf("proposal: %+v", p)
	}
	if pd := execute[engine.PlanDay](t, s, "plan_get", map[string]any{"day": d}); len(pd.Proposals) != 1 || len(pd.Items) != 2 {
		t.Fatalf("a proposal changes nothing until accepted: %+v", pd)
	}
	ap := execute[engine.PlanProposal](t, s, "plan_proposal_apply", map[string]any{"idempotency_key": key(), "id": p.ID, "expected_version": 1})
	if ap.State != "applied" {
		t.Fatalf("applied: %+v", ap)
	}
	pd := execute[engine.PlanDay](t, s, "plan_get", map[string]any{"day": d})
	if len(pd.Items) != 1 || pd.Items[0].Title != "Take a walk" || len(pd.Proposals) != 0 {
		t.Fatalf("after apply: %+v", pd.Items)
	}
	if tm := execute[engine.PlanDay](t, s, "plan_get", map[string]any{"day": tomorrow}); len(tm.Items) != 1 || tm.Items[0].ID != a.ID {
		t.Fatalf("moved item: %+v", tm.Items)
	}
	errorCode(t, s, "plan_proposal_apply", map[string]any{"idempotency_key": key(), "id": p.ID, "expected_version": 2}, "conflict")

	// A proposal goes stale if the plan moved on; nothing is half-applied.
	c := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Call bank"})
	p2 := execute[engine.PlanProposal](t, s, "plan_propose", map[string]any{"idempotency_key": key(), "day": d, "reason": "Busy day", "changes": []map[string]any{
		{"action": "add", "title": "Should not appear"}, {"action": "drop", "item_id": c.ID}}})
	execute[engine.PlanItem](t, s, "plan_items_delete", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 1})
	errorCode(t, s, "plan_proposal_apply", map[string]any{"idempotency_key": key(), "id": p2.ID, "expected_version": 1}, "conflict")
	for _, it := range execute[engine.PlanDay](t, s, "plan_get", map[string]any{"day": d}).Items {
		if it.Title == "Should not appear" {
			t.Fatal("half-applied proposal")
		}
	}
	disc := execute[engine.PlanProposal](t, s, "plan_proposal_discard", map[string]any{"idempotency_key": key(), "id": p2.ID, "expected_version": 1})
	if disc.State != "discarded" {
		t.Fatalf("discarded: %+v", disc)
	}
	if l := execute[engine.Page[engine.PlanProposal]](t, s, "plan_proposals_list", map[string]any{"state": "all"}); l.Total != 2 {
		t.Fatalf("list all: %d", l.Total)
	}
}

func TestRoutines(t *testing.T) {
	s, pool := integrationEnv(t)
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Morning run", "minutes": 30, "short_title": "Walk around the block", "short_minutes": 10})
	if r.TimeZone != "UTC" || len(r.Weekdays) != 0 || r.Version != 1 {
		t.Fatalf("created: %+v", r)
	}
	errorCode(t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "x", "weekdays": []int{9}}, "invalid_input")
	errorCode(t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "day": "2999-01-01", "variant": "full"}, "invalid_input")

	st := execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "variant": "short"})
	if !st.Done || st.Variant != "short" || st.Streak != 1 || st.Gap {
		t.Fatalf("checked in: %+v", st)
	}
	// Idempotent per day: a second call under a new key replaces rather than duplicating.
	st = execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "variant": "full"})
	if st.Variant != "full" || st.Streak != 1 {
		t.Fatalf("replaced: %+v", st)
	}
	y := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "day": y, "variant": "full"})
	if all := execute[engine.RoutinesStatus](t, s, "routines_status", map[string]any{}); all.Items[0].Streak != 2 {
		t.Fatalf("two-day streak: %+v", all.Items[0])
	}
	cleared := execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "variant": "none"})
	if cleared.Done || cleared.Streak != 1 {
		t.Fatalf("cleared today keeps yesterday's streak: %+v", cleared)
	}

	// A gap of three days produces a restart hint and one notice.
	pool.Exec(t.Context(), "DELETE FROM life.routine_checkins")
	old := time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02")
	execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "day": old, "variant": "full"})
	gap := execute[engine.RoutinesStatus](t, s, "routines_status", map[string]any{}).Items[0]
	if !gap.Gap || gap.DaysSinceLast == nil || *gap.DaysSinceLast != 5 || gap.Streak != 0 {
		t.Fatalf("gap: %+v", gap)
	}
	ev := execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items
	if len(ev) != 1 || ev[0].EventKey != "life:routine_gap" || ev[0].Notice.Link.ID != r.ID {
		t.Fatalf("gap notice: %+v", ev)
	}
	execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{ev[0].EventID}})
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("gap notice repeated: %d", n)
	}
}

// The nudge at the chosen time is a wake (wake_integration_test.go), not a feed event.
func TestRoutineNudgeIsNotAFeedEvent(t *testing.T) {
	s := integrationService(t)
	execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Stretch", "cues": []string{"00:00"}})
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("a routine nudge must not be a feed event: %d", n)
	}
}
