package engine

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type Step struct {
	ID        string     `json:"id"`
	GoalID    string     `json:"goal_id"`
	Title     string     `json:"title"`
	Minutes   int        `json:"minutes"`
	Position  int        `json:"position" jsonschema:"1-based order within the goal"`
	DoneAt    *time.Time `json:"done_at"`
	Version   int64      `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type GoalProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Goal always carries its steps, so a screen needs one read.
type Goal struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Why          string       `json:"why"`
	WeeklyTarget int          `json:"weekly_target" jsonschema:"steps the person aims to finish each week"`
	ArchivedAt   *time.Time   `json:"archived_at"`
	Version      int64        `json:"version"`
	Steps        []Step       `json:"steps"`
	Progress     GoalProgress `json:"progress"`
	NextStep     *Step        `json:"next_step" jsonschema:"the first step not yet done, or null"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type StepData struct {
	Title   string `json:"title" jsonschema:"one small, concrete action, 1-200 characters"`
	Minutes int    `json:"minutes,omitempty" jsonschema:"how long it should take, 1-480, default 15"`
}
type GoalData struct {
	Title        string `json:"title" jsonschema:"what the person wants to make progress on, 1-200 characters"`
	Why          string `json:"why,omitempty" jsonschema:"why it matters, up to 1000 characters"`
	WeeklyTarget *int   `json:"weekly_target,omitempty" jsonschema:"steps to finish each week, 0-21, default 3"`
}
type CreateGoalInput struct {
	Meta
	ID string `json:"id,omitempty"`
	GoalData
	Steps []StepData `json:"steps,omitempty" jsonschema:"optional starting steps in order; add more later with steps_add"`
}
type UpdateGoalInput struct {
	EditInput
	GoalData
}
type ListGoalsInput struct {
	ListInput
}
type AddStepInput struct {
	Meta
	ID       string `json:"id,omitempty"`
	GoalID   string `json:"goal_id"`
	Position *int   `json:"position,omitempty" jsonschema:"1-based place to insert; default the end"`
	StepData
}
type UpdateStepInput struct {
	EditInput
	StepData
}
type CompleteInput struct {
	EditInput
	Done bool `json:"done" jsonschema:"true marks it done now; false reopens it"`
}
type ReorderStepsInput struct {
	Meta
	GoalID  string   `json:"goal_id"`
	StepIDs []string `json:"step_ids" jsonschema:"every step of the goal, in the new order"`
}

const goalCols = `id::text,title,why,weekly_target,archived_at,version,created_at,updated_at`
const stepCols = `id::text,goal_id::text,title,minutes,position,done_at,version,created_at,updated_at`

func scanStep(row scanner) (Step, error) {
	var s Step
	err := row.Scan(&s.ID, &s.GoalID, &s.Title, &s.Minutes, &s.Position, &s.DoneAt, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	utcPtr(s.DoneAt)
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return s, err
}

func utcPtr(t *time.Time) {
	if t != nil {
		*t = t.UTC()
	}
}

func (g *Goal) fill(steps []Step) {
	g.Steps = steps
	if g.Steps == nil {
		g.Steps = []Step{}
	}
	g.Progress = GoalProgress{Total: len(g.Steps)}
	g.NextStep = nil
	for i := range g.Steps {
		if g.Steps[i].DoneAt != nil {
			g.Progress.Done++
		} else if g.NextStep == nil {
			g.NextStep = &g.Steps[i]
		}
	}
}

func (u *unit) goals(ctx context.Context, where string, args ...any) ([]Goal, error) {
	rows, err := u.tx.Query(ctx, "SELECT "+goalCols+" FROM life.goals "+where, args...)
	if err != nil {
		return nil, err
	}
	var goals []Goal
	index := map[string]int{}
	var ids []string
	for rows.Next() {
		var g Goal
		if err := rows.Scan(&g.ID, &g.Title, &g.Why, &g.WeeklyTarget, &g.ArchivedAt, &g.Version, &g.CreatedAt, &g.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		utcPtr(g.ArchivedAt)
		g.CreatedAt, g.UpdatedAt = g.CreatedAt.UTC(), g.UpdatedAt.UTC()
		index[g.ID] = len(goals)
		ids = append(ids, g.ID)
		goals = append(goals, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return goals, nil
	}
	srows, err := u.tx.Query(ctx, "SELECT "+stepCols+" FROM life.steps WHERE goal_id = ANY($1::uuid[]) ORDER BY goal_id, position", ids)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	steps := map[string][]Step{}
	for srows.Next() {
		s, err := scanStep(srows)
		if err != nil {
			return nil, err
		}
		steps[s.GoalID] = append(steps[s.GoalID], s)
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	for i := range goals {
		goals[i].fill(steps[goals[i].ID])
	}
	return goals, nil
}

func (u *unit) getGoal(ctx context.Context, key string, forUpdate bool) (Goal, error) {
	k, e := id(key)
	if e != nil {
		return Goal{}, e
	}
	if forUpdate {
		var locked string
		if e = u.tx.QueryRow(ctx, "SELECT id::text FROM life.goals WHERE id=$1 FOR UPDATE", k).Scan(&locked); e != nil {
			return Goal{}, e
		}
	}
	list, e := u.goals(ctx, "WHERE id=$1", k)
	if e != nil {
		return Goal{}, e
	}
	if len(list) == 0 {
		return Goal{}, notFound()
	}
	return list[0], nil
}

func (d GoalData) values() (title, why string, target int, err error) {
	title, why, target = strings.TrimSpace(d.Title), d.Why, 3
	if title == "" || len(title) > 200 {
		return "", "", 0, invalid("title must be 1-200 characters")
	}
	if len(why) > 1000 {
		return "", "", 0, invalid("why must be at most 1000 characters")
	}
	if d.WeeklyTarget != nil {
		target = *d.WeeklyTarget
	}
	if target < 0 || target > 21 {
		return "", "", 0, invalid("weekly_target must be 0-21")
	}
	return
}

func (d StepData) values() (string, int, error) {
	title, minutes := strings.TrimSpace(d.Title), d.Minutes
	if title == "" || len(title) > 200 {
		return "", 0, invalid("step title must be 1-200 characters")
	}
	if minutes == 0 {
		minutes = 15
	}
	if minutes < 1 || minutes > 480 {
		return "", 0, invalid("step minutes must be 1-480")
	}
	return title, minutes, nil
}

func createGoal(ctx context.Context, u *unit, in CreateGoalInput) (Goal, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Goal{}, e
	}
	title, why, target, e := in.GoalData.values()
	if e != nil {
		return Goal{}, e
	}
	if len(in.Steps) > 50 {
		return Goal{}, invalid("at most 50 starting steps")
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.goals(id,title,why,weekly_target) VALUES($1,$2,$3,$4)`, key, title, why, target); e != nil {
		return Goal{}, e
	}
	for i, sd := range in.Steps {
		st, minutes, e := sd.values()
		if e != nil {
			return Goal{}, e
		}
		var sid string
		if e = u.tx.QueryRow(ctx, `INSERT INTO life.steps(goal_id,title,minutes,position) VALUES($1,$2,$3,$4) RETURNING id::text`, key, st, minutes, i+1).Scan(&sid); e != nil {
			return Goal{}, e
		}
		created, e := u.getStep(ctx, sid, false)
		if e != nil {
			return Goal{}, e
		}
		if e = u.audit(ctx, "step", sid, nil, created); e != nil {
			return Goal{}, e
		}
	}
	g, e := u.getGoal(ctx, key, false)
	if e != nil {
		return g, e
	}
	return g, u.audit(ctx, "goal", key, nil, g)
}

func updateGoal(ctx context.Context, u *unit, in UpdateGoalInput) (Goal, error) {
	old, e := u.getGoal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.ArchivedAt != nil {
		return old, conflict("Restore the goal before editing it")
	}
	title, why, target, e := in.GoalData.values()
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.goals SET title=$2,why=$3,weekly_target=$4,version=version+1,updated_at=now() WHERE id=$1`, old.ID, title, why, target); e != nil {
		return old, e
	}
	return u.goalChanged(ctx, old)
}

func (u *unit) goalChanged(ctx context.Context, old Goal) (Goal, error) {
	g, e := u.getGoal(ctx, old.ID, false)
	if e != nil {
		return g, e
	}
	return g, u.audit(ctx, "goal", g.ID, old, g)
}

func lifecycleGoal(ctx context.Context, u *unit, in EditInput, restore bool) (Goal, error) {
	old, e := u.getGoal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.ArchivedAt == nil) {
		if restore {
			return old, conflict("Goal is not archived")
		}
		return old, conflict("Goal is already archived")
	}
	set := "archived_at=now()"
	if restore {
		set = "archived_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.goals SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.goalChanged(ctx, old)
}

func listGoals(ctx context.Context, u *unit, in ListGoalsInput) (Page[Goal], error) {
	result := Page[Goal]{Items: []Goal{}}
	limit, offset, state, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	where, args := []string{"true"}, []any{}
	switch state { // for goals "deleted" means archived
	case "active":
		where = append(where, "archived_at IS NULL")
	case "deleted":
		where = append(where, "archived_at IS NOT NULL")
	}
	if in.Search != "" {
		args = append(args, like(in.Search))
		where = append(where, "(title ILIKE $1 OR why ILIKE $1)")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.goals WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	args = append(args, limit, offset)
	goals, e := u.goals(ctx, "WHERE "+clause+" ORDER BY created_at, id LIMIT $"+strconv.Itoa(len(args)-1)+" OFFSET $"+strconv.Itoa(len(args)), args...)
	if e != nil {
		return result, e
	}
	if goals != nil {
		result.Items = goals
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}

// ---- steps

func (u *unit) getStep(ctx context.Context, key string, forUpdate bool) (Step, error) {
	k, e := id(key)
	if e != nil {
		return Step{}, e
	}
	q := "SELECT " + stepCols + " FROM life.steps WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanStep(u.tx.QueryRow(ctx, q, k))
}

func addStep(ctx context.Context, u *unit, in AddStepInput) (Step, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Step{}, e
	}
	goal, e := u.getGoal(ctx, in.GoalID, true)
	if e != nil {
		return Step{}, e
	}
	if goal.ArchivedAt != nil {
		return Step{}, conflict("Restore the goal before adding steps")
	}
	title, minutes, e := in.StepData.values()
	if e != nil {
		return Step{}, e
	}
	if len(goal.Steps) >= 100 {
		return Step{}, invalid("a goal can have at most 100 steps")
	}
	pos := len(goal.Steps) + 1
	if in.Position != nil {
		if *in.Position < 1 || *in.Position > pos {
			return Step{}, invalid("position must be 1-" + strconv.Itoa(pos))
		}
		pos = *in.Position
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.steps SET position=position+1 WHERE goal_id=$1 AND position>=$2`, goal.ID, pos); e != nil {
		return Step{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.steps(id,goal_id,title,minutes,position) VALUES($1,$2,$3,$4,$5)`, key, goal.ID, title, minutes, pos); e != nil {
		return Step{}, e
	}
	s, e := u.getStep(ctx, key, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "step", key, nil, s)
}

func (u *unit) stepChanged(ctx context.Context, old Step) (Step, error) {
	s, e := u.getStep(ctx, old.ID, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "step", s.ID, old, s)
}

func updateStep(ctx context.Context, u *unit, in UpdateStepInput) (Step, error) {
	old, e := u.getStep(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	title, minutes, e := in.StepData.values()
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.steps SET title=$2,minutes=$3,version=version+1,updated_at=now() WHERE id=$1`, old.ID, title, minutes); e != nil {
		return old, e
	}
	return u.stepChanged(ctx, old)
}

func (u *unit) setStepDone(ctx context.Context, step Step, done bool) (Step, error) {
	if (step.DoneAt != nil) == done {
		return step, nil
	}
	val := "NULL"
	if done {
		val = "now()"
	}
	if _, e := u.tx.Exec(ctx, `UPDATE life.steps SET done_at=`+val+`,version=version+1,updated_at=now() WHERE id=$1`, step.ID); e != nil {
		return step, e
	}
	return u.stepChanged(ctx, step)
}

func completeStep(ctx context.Context, u *unit, in CompleteInput) (Step, error) {
	old, e := u.getStep(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	return u.setStepDone(ctx, old, in.Done)
}

func deleteStep(ctx context.Context, u *unit, in EditInput) (Step, error) {
	old, e := u.getStep(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `DELETE FROM life.steps WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.steps SET position=position-1 WHERE goal_id=$1 AND position>$2`, old.GoalID, old.Position); e != nil {
		return old, e
	}
	// The audit row keeps the deleted step; there is no restore for a step, add it again.
	return old, u.audit(ctx, "step", old.ID, old, nil)
}

func reorderSteps(ctx context.Context, u *unit, in ReorderStepsInput) (Goal, error) {
	old, e := u.getGoal(ctx, in.GoalID, true)
	if e != nil {
		return old, e
	}
	if len(in.StepIDs) != len(old.Steps) {
		return old, invalid("step_ids must list every step of the goal exactly once")
	}
	have := map[string]bool{}
	for _, s := range old.Steps {
		have[s.ID] = true
	}
	for i, raw := range in.StepIDs {
		sid, e := id(raw)
		if e != nil || !have[sid] {
			return old, invalid("step_ids must list every step of the goal exactly once")
		}
		delete(have, sid)
		if _, e = u.tx.Exec(ctx, `UPDATE life.steps SET position=$2,updated_at=now() WHERE id=$1`, sid, i+1); e != nil {
			return old, e
		}
	}
	return u.goalChanged(ctx, old)
}
