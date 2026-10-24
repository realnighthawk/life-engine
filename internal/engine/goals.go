package engine

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// A goal is an outcome the person's tasks and events serve: finite ("Lisbon, June 3-10") with an end date, open-ended
// ("get fit") without one. It is also the container for a composed use case: a trip or a dinner is a goal whose tasks and
// events link to it. Nothing about a kind of goal is stored; progress is read from what is linked, never kept.
type Goal struct {
	ID        string     `json:"id"`
	Subject   string     `json:"subject" jsonschema:"person (the default) or agent: whose goal it is"`
	Title     string     `json:"title"`
	Notes     string     `json:"notes" jsonschema:"short free text kept with the goal, such as take the coolers or watch the weather"`
	EndsOn    *string    `json:"ends_on" jsonschema:"local date the goal finishes by; null for an open-ended goal"`
	Progress  Progress   `json:"progress" jsonschema:"read from the tasks linked to the goal"`
	Next      *Item      `json:"next" jsonschema:"the linked open task that comes first, soonest time first, or null"`
	Version   int64      `json:"version"`
	DeletedAt *time.Time `json:"deleted_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Progress counts a goal's one-off tasks and the occurrences of its repeating ones that have been done. Events are not
// counted: they happen, they are not done.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type GoalData struct {
	Title   string  `json:"title" jsonschema:"the outcome, 1-200 characters, in the person's words"`
	Notes   string  `json:"notes,omitempty" jsonschema:"short free text, up to 2000 characters"`
	EndsOn  *string `json:"ends_on,omitempty" jsonschema:"YYYY-MM-DD the goal finishes by; omit for an open-ended goal"`
	Subject string  `json:"subject,omitempty" jsonschema:"person (default) or agent; agent is for a goal of your own, which the person's screens do not show"`
}
type CreateGoalInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to this goal"`
	GoalData
}
type UpdateGoalInput struct {
	EditInput
	GoalData
}
type ListGoalsInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
}

const goalCols = `id::text,subject,title,notes,ends_on::text,version,deleted_at,created_at,updated_at`

func scanGoal(row scanner) (Goal, error) {
	var g Goal
	if err := row.Scan(&g.ID, &g.Subject, &g.Title, &g.Notes, &g.EndsOn, &g.Version, &g.DeletedAt, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return g, err
	}
	if g.DeletedAt != nil {
		*g.DeletedAt = g.DeletedAt.UTC()
	}
	g.CreatedAt, g.UpdatedAt = g.CreatedAt.UTC(), g.UpdatedAt.UTC()
	return g, nil
}

// withProgress fills in what is derived from the linked items.
func (u *unit) withProgress(ctx context.Context, g Goal) (Goal, error) {
	err := u.tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM life.items WHERE goal_id=$1 AND kind='task' AND deleted_at IS NULL AND recurrence IS NULL AND done_at IS NOT NULL)
		+ (SELECT count(*) FROM life.occurrences o JOIN life.items i ON i.id=o.item_id WHERE i.goal_id=$1 AND i.kind='task' AND i.deleted_at IS NULL AND o.state='done'),
		(SELECT count(*) FROM life.items WHERE goal_id=$1 AND kind='task' AND deleted_at IS NULL AND recurrence IS NULL)
		+ (SELECT count(*) FROM life.occurrences o JOIN life.items i ON i.id=o.item_id WHERE i.goal_id=$1 AND i.kind='task' AND i.deleted_at IS NULL)`, g.ID).Scan(&g.Progress.Done, &g.Progress.Total)
	if err != nil {
		return g, err
	}
	row := u.tx.QueryRow(ctx, "SELECT "+itemCols+" FROM life.items WHERE goal_id=$1 AND kind='task' AND deleted_at IS NULL AND recurrence IS NULL AND done_at IS NULL ORDER BY at ASC NULLS LAST, created_at, id LIMIT 1", g.ID)
	next, err := scanItem(row)
	switch {
	case err == nil:
		g.Next = &next
	case isNoRows(err):
	default:
		return g, err
	}
	return g, nil
}

func (u *unit) getGoal(ctx context.Context, key string, forUpdate bool) (Goal, error) {
	k, e := u.resolveRef(ctx, "goals", "", key)
	if e != nil {
		return Goal{}, e
	}
	q := "SELECT " + goalCols + " FROM life.goals WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	g, e := scanGoal(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "goals", "", k)
	}
	if e != nil {
		return g, e
	}
	return u.withProgress(ctx, g)
}

type goalValues struct {
	title, notes, subject string
	endsOn                *string
}

func (d GoalData) values() (goalValues, error) {
	v := goalValues{title: strings.TrimSpace(d.Title), notes: d.Notes, subject: d.Subject, endsOn: d.EndsOn}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if len(v.notes) > 2000 {
		return v, invalid("notes must be at most 2000 characters")
	}
	if v.subject == "" {
		v.subject = "person"
	}
	if !oneOf(v.subject, "person", "agent") {
		return v, invalid("subject must be person or agent")
	}
	if v.endsOn != nil {
		if _, e := day("ends_on", *v.endsOn); e != nil {
			return v, e
		}
	}
	return v, nil
}

func createGoal(ctx context.Context, u *unit, in CreateGoalInput) (Goal, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Goal{}, e
	}
	v, e := in.GoalData.values()
	if e != nil {
		return Goal{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.goals(id,subject,title,notes,ends_on) VALUES($1,$2,$3,$4,$5::date)`, key, v.subject, v.title, v.notes, v.endsOn); e != nil {
		return Goal{}, e
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
	if old.DeletedAt != nil {
		return old, conflict("Restore the goal before editing it")
	}
	v, e := in.GoalData.values()
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.goals SET title=$2,notes=$3,ends_on=$4::date,subject=$5,version=version+1,updated_at=now() WHERE id=$1`, old.ID, v.title, v.notes, v.endsOn, v.subject); e != nil {
		return old, e
	}
	g, e := u.getGoal(ctx, old.ID, false)
	if e != nil {
		return g, e
	}
	return g, u.audit(ctx, "goal", g.ID, old, g)
}

// lifecycleGoal archives or restores a goal. What is linked to it stays, so restoring brings the whole back.
func lifecycleGoal(ctx context.Context, u *unit, in EditInput, restore bool) (Goal, error) {
	old, e := u.getGoal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Goal is not archived")
		}
		return old, conflict("Goal is already archived")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.goals SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	g, e := u.getGoal(ctx, old.ID, false)
	if e != nil {
		return g, e
	}
	return g, u.audit(ctx, "goal", g.ID, old, g)
}

func listGoals(ctx context.Context, u *unit, in ListGoalsInput) (Page[Goal], error) {
	result := Page[Goal]{Items: []Goal{}}
	limit, offset, state, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	subject := in.Subject
	if subject == "" {
		subject = "person"
	}
	if !oneOf(subject, "person", "agent") {
		return result, invalid("subject must be person or agent")
	}
	where, args := []string{"subject=$1"}, []any{subject}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if in.Search != "" {
		p := arg(like(in.Search))
		where = append(where, "(title ILIKE "+p+" OR notes ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.goals WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+goalCols+" FROM life.goals WHERE "+clause+" ORDER BY ends_on ASC NULLS LAST, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	var goals []Goal
	for rows.Next() {
		g, e := scanGoal(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		goals = append(goals, g)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	for _, g := range goals {
		if g, e = u.withProgress(ctx, g); e != nil {
			return result, e
		}
		result.Items = append(result.Items, g)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}
