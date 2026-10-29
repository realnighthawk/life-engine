package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// A task waits for someone to do it. A time on it is a deadline or a cue ("call Sam at 5"), not an appointment; repeat
// makes it a routine and an alert makes it a reminder. A one-off task finishes as a whole; a repeating one finishes one
// occurrence at a time, and each completion is an activity linked to the task.
type Task struct {
	ID                 string          `json:"id"`
	Subject            string          `json:"subject" jsonschema:"person (the default) or agent"`
	Title              string          `json:"title"`
	Notes              string          `json:"notes"`
	At                 *time.Time      `json:"at" jsonschema:"a deadline or cue, or null for a task with no time"`
	Minutes            *int            `json:"minutes" jsonschema:"how long it takes, or null"`
	TimeZone           string          `json:"time_zone"`
	Recurrence         *Recurrence     `json:"recurrence" jsonschema:"how it repeats, or null for a one-off"`
	AlertBeforeMinutes *int            `json:"alert_before_minutes" jsonschema:"tell the owner this many minutes before at, or null"`
	IsPriority         bool            `json:"is_priority"`
	SessionKey         string          `json:"session_key,omitempty" jsonschema:"for an agent's task, the conversation its alert wakes"`
	SnoozedUntil       *time.Time      `json:"snoozed_until"`
	DoneAt             *time.Time      `json:"done_at" jsonschema:"when a one-off task was done; a repeating task is done one occurrence at a time"`
	Result             string          `json:"result,omitempty" jsonschema:"for a done one-off task: the line its owner gave when completing it"`
	Origin             string          `json:"origin" jsonschema:"person or agent: who authored it"`
	DerivedFrom        json.RawMessage `json:"derived_from,omitempty"`
	Version            int64           `json:"version"`
	DeletedAt          *time.Time      `json:"deleted_at"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type TaskData struct {
	TimingData
	Title      string `json:"title" jsonschema:"1-200 characters"`
	Notes      string `json:"notes,omitempty" jsonschema:"short detail, up to 2000 characters"`
	IsPriority bool   `json:"is_priority,omitempty"`
}
type CreateTaskInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	TaskData
}
type UpdateTaskInput struct {
	EditInput
	TaskData
}
type CompleteTaskInput struct {
	EditInput
	At     *string `json:"at,omitempty" jsonschema:"RFC 3339 occurrence being done, for a repeating task; default the one now due, else the next"`
	Result string  `json:"result,omitempty" jsonschema:"one line on what you found or did, up to 2000 characters"`
	Actor  string  `json:"actor,omitempty" jsonschema:"person (default) or agent: who did it"`
	Done   *bool   `json:"done,omitempty" jsonschema:"default true; false reopens a task that was marked done (the named occurrence, for a repeating one)"`
}
type SnoozeTaskInput struct {
	EditInput
	Until string `json:"until" jsonschema:"RFC 3339 instant in the future the alert is deferred to"`
}
type ListTasksInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Status  string `json:"status,omitempty" jsonschema:"open (default), done or all"`
	After   string `json:"after,omitempty" jsonschema:"RFC 3339; only tasks at or after this time"`
	Before  string `json:"before,omitempty" jsonschema:"RFC 3339; only tasks before this time"`
}

const taskCols = `id::text,subject,title,notes,at,minutes,time_zone,recurrence,alert_before_minutes,is_priority,coalesce(session_key,''),snoozed_until,done_at,result,origin,derived_from,version,deleted_at,created_at,updated_at`

func scanTask(row scanner) (Task, error) {
	var t Task
	var rec, derived []byte
	err := row.Scan(&t.ID, &t.Subject, &t.Title, &t.Notes, &t.At, &t.Minutes, &t.TimeZone, &rec, &t.AlertBeforeMinutes,
		&t.IsPriority, &t.SessionKey, &t.SnoozedUntil, &t.DoneAt, &t.Result, &t.Origin, &derived, &t.Version, &t.DeletedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	if len(rec) > 0 {
		t.Recurrence = new(Recurrence)
		if err = json.Unmarshal(rec, t.Recurrence); err != nil {
			return t, err
		}
	}
	if len(derived) > 0 {
		t.DerivedFrom = json.RawMessage(derived)
	}
	for _, p := range []*time.Time{t.At, t.SnoozedUntil, t.DoneAt, t.DeletedAt} {
		utcPtr(p)
	}
	t.CreatedAt, t.UpdatedAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC()
	return t, nil
}

func (u *unit) getTask(ctx context.Context, key string, forUpdate bool) (Task, error) {
	k, e := u.resolveRef(ctx, "tasks", "", key)
	if e != nil {
		return Task{}, e
	}
	q := "SELECT " + taskCols + " FROM life.tasks WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	t, e := scanTask(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "tasks", "", k)
	}
	return t, e
}

func createTask(ctx context.Context, u *unit, in CreateTaskInput) (Task, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Task{}, e
	}
	v, e := in.TimingData.values(false)
	if e != nil {
		return Task{}, e
	}
	title, notes, e := trimTitleNotes(in.Title, in.Notes)
	if e != nil {
		return Task{}, e
	}
	session, e := u.sessionFor(v)
	if e != nil {
		return Task{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.tasks(id,subject,title,notes,at,minutes,time_zone,recurrence,alert_before_minutes,
		is_priority,session_key,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13)`,
		key, v.subject, title, notes, v.at, v.minutes, v.tz, jsonOrNil(v.rec), v.alert, in.IsPriority, session, v.origin, v.derived); e != nil {
		return Task{}, e
	}
	t, e := u.getTask(ctx, key, false)
	if e != nil {
		return t, e
	}
	return t, u.audit(ctx, "task", key, nil, t)
}

func updateTask(ctx context.Context, u *unit, in UpdateTaskInput) (Task, error) {
	old, e := u.getTask(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the task before editing it")
	}
	v, e := in.TimingData.values(false)
	if e != nil {
		return old, e
	}
	title, notes, e := trimTitleNotes(in.Title, in.Notes)
	if e != nil {
		return old, e
	}
	session, e := u.sessionFor(v)
	if e != nil {
		return old, e
	}
	if session == "" {
		session = old.SessionKey
	}
	// Editing the time starts a fresh occurrence, so a snooze no longer applies.
	if _, e = u.tx.Exec(ctx, `UPDATE life.tasks SET subject=$2,title=$3,notes=$4,at=$5,minutes=$6,time_zone=$7,recurrence=$8,
		alert_before_minutes=$9,is_priority=$10,session_key=NULLIF($11,''),origin=$12,derived_from=$13,snoozed_until=NULL,
		version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.subject, title, notes, v.at, v.minutes, v.tz, jsonOrNil(v.rec), v.alert, in.IsPriority, session, v.origin, v.derived); e != nil {
		return old, e
	}
	return u.taskChanged(ctx, old)
}

func (u *unit) taskChanged(ctx context.Context, old Task) (Task, error) {
	t, e := u.getTask(ctx, old.ID, false)
	if e != nil {
		return t, e
	}
	return t, u.audit(ctx, "task", t.ID, old, t)
}

// completeTask finishes a one-off task, or one occurrence of a repeating one. The evidence is an activity linked to the
// task, so reopening removes that occurrence's evidence rather than editing history in place.
func completeTask(ctx context.Context, u *unit, in CompleteTaskInput) (Task, error) {
	old, e := u.getTask(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the task before completing it")
	}
	if len(in.Result) > 2000 {
		return old, invalid("result must be at most 2000 characters")
	}
	actor := in.Actor
	if actor == "" {
		actor = "person"
	}
	if !oneOf(actor, "person", "agent") {
		return old, invalid("actor must be person or agent")
	}
	now, e := u.now(ctx)
	if e != nil {
		return old, e
	}
	if in.Done != nil && !*in.Done {
		return reopenTask(ctx, u, old, in, now)
	}
	if old.Recurrence == nil {
		if old.DoneAt != nil {
			return old, conflict("Task is already done")
		}
		occurrence := now
		if old.At != nil {
			occurrence = old.At.UTC().Truncate(time.Second)
		}
		if _, e = u.tx.Exec(ctx, `UPDATE life.tasks SET done_at=$2,result=$3,snoozed_until=NULL,version=version+1,updated_at=now() WHERE id=$1`, old.ID, now, in.Result); e != nil {
			return old, e
		}
		if e = u.recordOccurrence(ctx, old, occurrence, in.Result, actor); e != nil {
			return old, e
		}
		done, e := u.taskChanged(ctx, old)
		if e != nil {
			return done, e
		}
		return done, u.taskDone(ctx, done)
	}
	var at time.Time
	if in.At != nil {
		if at, e = instant("at", *in.At); e != nil {
			return old, e
		}
	} else {
		loc, _ := zone(old.TimeZone)
		at = latestOccurrenceAtOrBefore(*old.At, now, *old.Recurrence, loc)
	}
	at = at.UTC().Truncate(time.Second)
	var exists bool
	if e = u.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM life.links l JOIN life.activities a ON a.id=l.from_id
		WHERE l.from_kind='activity' AND l.relation='fulfils' AND l.to_kind='task' AND l.to_id=$1 AND a.occurred_at=$2)`, old.ID, at).Scan(&exists); e != nil {
		return old, e
	}
	if !exists {
		if e = u.recordOccurrence(ctx, old, at, in.Result, actor); e != nil {
			return old, e
		}
	}
	return old, u.audit(ctx, "task", old.ID, nil, map[string]any{"occurrence": at, "state": "done", "result": in.Result})
}

// recordOccurrence writes the activity that proves an occurrence was done and links it to the task.
func (u *unit) recordOccurrence(ctx context.Context, t Task, at time.Time, result, actor string) error {
	text := result
	if text == "" {
		text = "Done: " + t.Title
	}
	a, e := u.insertActivity(ctx, "", t.Subject, actor, "occurrence", text, at, actor, t.Origin, t.DerivedFrom)
	if e != nil {
		return e
	}
	_, e = u.tx.Exec(ctx, `INSERT INTO life.links(from_kind,from_id,relation,to_kind,to_id) VALUES('activity',$1,'fulfils','task',$2)
		ON CONFLICT DO NOTHING`, a.ID, t.ID)
	return e
}

func reopenTask(ctx context.Context, u *unit, old Task, in CompleteTaskInput, now time.Time) (Task, error) {
	occurrence := now
	if old.Recurrence == nil {
		if old.DoneAt == nil {
			return old, conflict("Task is not done")
		}
		if old.At != nil {
			occurrence = old.At.UTC().Truncate(time.Second)
		} else if old.DoneAt != nil {
			occurrence = old.DoneAt.UTC().Truncate(time.Second)
		}
		if _, e := u.tx.Exec(ctx, `UPDATE life.tasks SET done_at=NULL,result='',version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
			return old, e
		}
	} else {
		if in.At != nil {
			var e error
			if occurrence, e = instant("at", *in.At); e != nil {
				return old, e
			}
		} else {
			loc, _ := zone(old.TimeZone)
			occurrence = latestOccurrenceAtOrBefore(*old.At, now, *old.Recurrence, loc)
		}
	}
	occurrence = occurrence.UTC().Truncate(time.Second)
	var ids []string
	rows, e := u.tx.Query(ctx, `SELECT l.from_id::text FROM life.links l JOIN life.activities a ON a.id=l.from_id
		WHERE l.from_kind='activity' AND l.relation='fulfils' AND l.to_kind='task' AND l.to_id=$1 AND a.occurred_at=$2`, old.ID, occurrence)
	if e != nil {
		return old, e
	}
	for rows.Next() {
		var activityID string
		if e = rows.Scan(&activityID); e != nil {
			rows.Close()
			return old, e
		}
		ids = append(ids, activityID)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return old, e
	}
	if len(ids) == 0 {
		return old, conflict("That occurrence is not done")
	}
	for _, activityID := range ids {
		if _, e = u.tx.Exec(ctx, `DELETE FROM life.links WHERE from_kind='activity' AND from_id=$1`, activityID); e != nil {
			return old, e
		}
		if _, e = u.tx.Exec(ctx, `DELETE FROM life.activities WHERE id=$1`, activityID); e != nil {
			return old, e
		}
	}
	return old, u.audit(ctx, "task", old.ID, nil, map[string]any{"occurrence": occurrence, "state": "reopened"})
}

// latestOccurrenceAtOrBefore is the occurrence now due: the last one not after now, or the first if the series has not
// started. It looks back a year, which is longer than any repeat the engine accepts.
func latestOccurrenceAtOrBefore(anchor, now time.Time, rule Recurrence, loc *time.Location) time.Time {
	start := now.AddDate(-1, 0, 0)
	if start.Before(anchor) {
		start = anchor
	}
	occ := occurrencesIn(anchor, rule, loc, start, now.Add(time.Second))
	if len(occ) == 0 {
		return anchor
	}
	return occ[len(occ)-1]
}

func snoozeTask(ctx context.Context, u *unit, in SnoozeTaskInput) (Task, error) {
	old, e := u.getTask(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	until, e := instant("until", in.Until)
	if e != nil {
		return old, e
	}
	now, e := u.now(ctx)
	if e != nil {
		return old, e
	}
	if !until.After(now) {
		return old, invalid("until must be in the future")
	}
	if old.DeletedAt != nil || old.DoneAt != nil {
		return old, conflict("Only an open task can be snoozed")
	}
	if old.AlertBeforeMinutes == nil {
		return old, invalid("This task has no alert to snooze")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.tasks SET snoozed_until=$2,version=version+1,updated_at=now() WHERE id=$1`, old.ID, until); e != nil {
		return old, e
	}
	return u.taskChanged(ctx, old)
}

func lifecycleTask(ctx context.Context, u *unit, in EditInput, restore bool) (Task, error) {
	old, e := u.getTask(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Task is not deleted")
		}
		return old, conflict("Task is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.tasks SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.taskChanged(ctx, old)
}

func listTasks(ctx context.Context, u *unit, in ListTasksInput) (Page[Task], error) {
	result := Page[Task]{Items: []Task{}}
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
	status := in.Status
	if status == "" {
		status = "open"
	}
	if !oneOf(status, "open", "done", "all") {
		return result, invalid("status must be open, done or all")
	}
	where, args := []string{"subject=$1"}, []any{subject}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if status != "all" {
		// A repeating task is never done as a whole; it stays open.
		if status == "open" {
			where = append(where, "done_at IS NULL")
		} else {
			where = append(where, "done_at IS NOT NULL")
		}
	}
	if in.Search != "" {
		p := arg(like(in.Search))
		where = append(where, "(title ILIKE "+p+" OR notes ILIKE "+p+")")
	}
	for _, bound := range []struct{ value, op string }{{in.After, ">="}, {in.Before, "<"}} {
		if bound.value == "" {
			continue
		}
		t, e := instant("after/before", bound.value)
		if e != nil {
			return result, e
		}
		where = append(where, "at "+bound.op+" "+arg(t))
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.tasks WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+taskCols+" FROM life.tasks WHERE "+clause+
		" ORDER BY at ASC NULLS LAST, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, t)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}
