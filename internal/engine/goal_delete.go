package engine

import "context"

// GoalDeletion is what deleting a goal removed. Everything is soft-deleted and audited like the single-record
// operations, except a step, which has no restore.
type GoalDeletion struct {
	Goal    Goal           `json:"goal"`
	Removed map[string]int `json:"removed" jsonschema:"how many of each kind of record went with the goal"`
}

// deleteGoal removes a goal and what exists only because of it: its steps, view, measurements, commitments, plan
// items, reminders linked to it, notes and journal entries written about it alone, and the routines no other goal uses.
// A routine another goal also uses, and a journal entry about several goals, only lose their link to this one.
func deleteGoal(ctx context.Context, u *unit, in EditInput) (GoalDeletion, error) {
	goal, e := u.getGoal(ctx, in.ID, true)
	if e != nil {
		return GoalDeletion{}, e
	}
	if e = version(in.ExpectedVersion, goal.Version); e != nil {
		return GoalDeletion{Goal: goal}, e
	}
	out := GoalDeletion{Removed: map[string]int{}}
	// each runs one removal per row the query returns, as the single-record operation would.
	each := func(kind, query string, remove func(EditInput) error) error {
		rows, e := u.tx.Query(ctx, query, goal.ID)
		if e != nil {
			return e
		}
		var found []EditInput
		for rows.Next() {
			var r EditInput
			if e = rows.Scan(&r.ID, &r.ExpectedVersion); e != nil {
				rows.Close()
				return e
			}
			found = append(found, r)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, r := range found {
			if e = remove(r); e != nil {
				return e
			}
			out.Removed[kind]++
		}
		return nil
	}
	steps := []struct {
		kind, query string
		remove      func(EditInput) error
	}{
		{"views", `SELECT id::text, version FROM life.views WHERE subject_kind='goal' AND subject_id=$1 AND deleted_at IS NULL`,
			func(r EditInput) error { _, e := deleteView(ctx, u, r); return e }},
		{"measurements", `SELECT id::text, version FROM life.goal_measurements WHERE goal_id=$1 AND deleted_at IS NULL`,
			func(r EditInput) error { _, e := deleteMeasurement(ctx, u, r); return e }},
		{"commitments", `SELECT id::text, version FROM life.commitments WHERE goal_id=$1`,
			func(r EditInput) error { _, e := deleteCommitment(ctx, u, r); return e }},
		{"plan_items", `SELECT id::text, version FROM life.plan_items WHERE (goal_id=$1 OR step_id IN (SELECT id FROM life.steps WHERE goal_id=$1)) AND deleted_at IS NULL`,
			func(r EditInput) error { _, e := lifecyclePlanItem(ctx, u, r, false); return e }},
		{"reminders", `SELECT id::text, version FROM life.reminders WHERE link->>'entity'='goal' AND link->>'id'=$1::text AND deleted_at IS NULL`,
			func(r EditInput) error { _, e := lifecycleReminder(ctx, u, r, false); return e }},
		{"notes", `SELECT id::text, version FROM life.notes WHERE goal_id=$1 AND deleted_at IS NULL`,
			func(r EditInput) error { _, e := lifecycleNote(ctx, u, r, false); return e }},
		{"journal_entries", `SELECT id::text, version FROM life.journal_entries WHERE goal_ids=ARRAY[$1::uuid] AND deleted_at IS NULL`,
			func(r EditInput) error { _, e := lifecycleJournal(ctx, u, r, false); return e }},
		{"routines", `SELECT r.id::text, r.version FROM life.goal_routines gr JOIN life.routines r ON r.id=gr.routine_id AND r.deleted_at IS NULL
			WHERE gr.goal_id=$1 AND NOT EXISTS (SELECT 1 FROM life.goal_routines o JOIN life.goals g ON g.id=o.goal_id AND g.archived_at IS NULL
				WHERE o.routine_id=r.id AND o.goal_id<>gr.goal_id)`,
			func(r EditInput) error { _, e := lifecycleRoutine(ctx, u, r, false); return e }},
		{"steps", `SELECT id::text, version FROM life.steps WHERE goal_id=$1 ORDER BY position DESC`,
			func(r EditInput) error { _, e := deleteStep(ctx, u, r); return e }},
	}
	for _, s := range steps {
		if e = each(s.kind, s.query, s.remove); e != nil {
			return GoalDeletion{Goal: goal}, e
		}
	}
	// What is left of the links: shared routines and multi-goal journal entries stay, without this goal.
	if _, e = u.tx.Exec(ctx, `DELETE FROM life.goal_routines WHERE goal_id=$1`, goal.ID); e != nil {
		return GoalDeletion{Goal: goal}, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.journal_entries SET goal_ids=array_remove(goal_ids,$1::uuid),version=version+1,updated_at=now() WHERE $1::uuid = ANY(goal_ids)`, goal.ID); e != nil {
		return GoalDeletion{Goal: goal}, e
	}
	if goal, e = u.getGoal(ctx, goal.ID, false); e != nil {
		return GoalDeletion{}, e
	}
	if goal.ArchivedAt == nil {
		if goal, e = lifecycleGoal(ctx, u, EditInput{ID: goal.ID, ExpectedVersion: goal.Version}, false); e != nil {
			return GoalDeletion{Goal: goal}, e
		}
	}
	out.Goal = goal
	return out, nil
}
