package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// An item is a task or an event. A task waits for someone to do it, so any time on it is a deadline or a cue ("call Sam at
// 5"). An event happens at its time whether or not anyone acts, and the person shows up. Either may repeat (a routine is a
// task that repeats) and either may alert (a reminder is an alert, not a thing of its own). Whose it is is its subject.
type Item struct {
	ID                 string      `json:"id"`
	Kind               string      `json:"kind" jsonschema:"task or event"`
	Subject            string      `json:"subject" jsonschema:"person (the default) or agent: whose it is"`
	Title              string      `json:"title"`
	Notes              string      `json:"notes"`
	GoalID             *string     `json:"goal_id" jsonschema:"the goal this serves, or null"`
	At                 *time.Time  `json:"at" jsonschema:"an event's start, or a task's deadline or cue; the first occurrence of a repeating item; null for a task with no time"`
	Minutes            *int        `json:"minutes" jsonschema:"how long it takes, or null"`
	TimeZone           string      `json:"time_zone"`
	Recurrence         *Recurrence `json:"recurrence" jsonschema:"how it repeats, or null for a one-off"`
	AlertBeforeMinutes *int        `json:"alert_before_minutes" jsonschema:"tell the owner this many minutes before at; 0 at the time itself; null for no alert"`
	IsPriority         bool        `json:"is_priority"`
	SessionKey         string      `json:"session_key,omitempty" jsonschema:"for an agent's item, the conversation its alert wakes"`
	SnoozedUntil       *time.Time  `json:"snoozed_until" jsonschema:"the alert is deferred to this time"`
	DoneAt             *time.Time  `json:"done_at" jsonschema:"when a one-off task was done; repeating items are done one occurrence at a time"`
	Version            int64       `json:"version"`
	DeletedAt          *time.Time  `json:"deleted_at"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

type ItemData struct {
	Title              string      `json:"title" jsonschema:"1-200 characters"`
	Notes              string      `json:"notes,omitempty" jsonschema:"short detail, up to 2000 characters"`
	GoalID             string      `json:"goal_id,omitempty" jsonschema:"UUID of the goal this serves; optional"`
	At                 *string     `json:"at,omitempty" jsonschema:"RFC 3339 instant: an event's start, or a task's deadline or cue. Required for an event"`
	Minutes            *int        `json:"minutes,omitempty" jsonschema:"how long it takes, 1-1440"`
	TimeZone           string      `json:"time_zone,omitempty" jsonschema:"IANA zone the person lives in (for example Europe/London); a repeat keeps this zone's wall-clock time. Default UTC"`
	Recurrence         *Recurrence `json:"recurrence,omitempty" jsonschema:"optional repeat; requires at"`
	AlertBeforeMinutes *int        `json:"alert_before_minutes,omitempty" jsonschema:"tell the owner this many minutes before at (0 = at the time), up to 10080; requires at. Omit for no alert"`
	IsPriority         bool        `json:"is_priority,omitempty"`
	Subject            string      `json:"subject,omitempty" jsonschema:"person (default), or agent for an item of your own, which the person's screens do not show; an agent's item with an alert wakes the conversation you create it from"`
}
type CreateItemInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to this record"`
	ItemData
}
type UpdateItemInput struct {
	EditInput
	ItemData
}
type CompleteItemInput struct {
	EditInput
	At     *string `json:"at,omitempty" jsonschema:"RFC 3339 occurrence being done, for a repeating task; default the one now due, else the next"`
	Result string  `json:"result,omitempty" jsonschema:"for your own tasks: one line on what you found or did, up to 2000 characters"`
	Done   *bool   `json:"done,omitempty" jsonschema:"default true; false reopens a task that was marked done (the named occurrence, for a repeating one)"`
}
type SnoozeItemInput struct {
	EditInput
	Until string `json:"until" jsonschema:"RFC 3339 instant in the future the alert is deferred to"`
}
type ListItemsInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	GoalID  string `json:"goal_id,omitempty" jsonschema:"only items serving this goal"`
	Status  string `json:"status,omitempty" jsonschema:"tasks only: open (default), done or all"`
	After   string `json:"after,omitempty" jsonschema:"RFC 3339; only items at or after this time"`
	Before  string `json:"before,omitempty" jsonschema:"RFC 3339; only items before this time"`
}

const itemCols = `id::text,kind,subject,title,notes,goal_id::text,at,minutes,time_zone,recurrence,alert_before_minutes,is_priority,coalesce(session_key,''),snoozed_until,done_at,version,deleted_at,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanItem(row scanner) (Item, error) {
	var it Item
	var rec []byte
	err := row.Scan(&it.ID, &it.Kind, &it.Subject, &it.Title, &it.Notes, &it.GoalID, &it.At, &it.Minutes, &it.TimeZone, &rec, &it.AlertBeforeMinutes,
		&it.IsPriority, &it.SessionKey, &it.SnoozedUntil, &it.DoneAt, &it.Version, &it.DeletedAt, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return it, err
	}
	for _, t := range []*time.Time{it.At, it.SnoozedUntil, it.DoneAt, it.DeletedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	it.CreatedAt, it.UpdatedAt = it.CreatedAt.UTC(), it.UpdatedAt.UTC()
	if len(rec) > 0 {
		it.Recurrence = new(Recurrence)
		err = json.Unmarshal(rec, it.Recurrence)
	}
	return it, err
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func (u *unit) getItem(ctx context.Context, kind, key string, forUpdate bool) (Item, error) {
	k, e := u.resolveRef(ctx, "items", kind, key)
	if e != nil {
		return Item{}, e
	}
	q := "SELECT " + itemCols + " FROM life.items WHERE id=$1 AND kind=$2"
	if forUpdate {
		q += " FOR UPDATE"
	}
	it, e := scanItem(u.tx.QueryRow(ctx, q, k, kind))
	if isNoRows(e) {
		e = u.missing(ctx, "items", kind, k)
	}
	return it, e
}

type itemValues struct {
	title, notes, goal, tz, subject string
	at                              *time.Time
	minutes, alert                  *int
	rec                             *Recurrence
	priority                        bool
}

func (d ItemData) values(kind string) (itemValues, error) {
	v := itemValues{title: strings.TrimSpace(d.Title), notes: d.Notes, goal: d.GoalID, tz: d.TimeZone, subject: d.Subject, minutes: d.Minutes, alert: d.AlertBeforeMinutes, priority: d.IsPriority}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if len(v.notes) > 2000 {
		return v, invalid("notes must be at most 2000 characters")
	}
	if v.tz == "" {
		v.tz = "UTC"
	}
	loc, e := zone(v.tz)
	if e != nil {
		return v, e
	}
	if v.subject == "" {
		v.subject = "person"
	}
	if !oneOf(v.subject, "person", "agent") {
		return v, invalid("subject must be person or agent")
	}
	if v.goal != "" {
		if v.goal, e = id(v.goal); e != nil {
			return v, invalid("goal_id must be a UUID")
		}
	}
	if v.minutes != nil && (*v.minutes < 1 || *v.minutes > 1440) {
		return v, invalid("minutes must be 1-1440")
	}
	if d.At != nil {
		t, e := instant("at", *d.At)
		if e != nil {
			return v, e
		}
		v.at = &t
	}
	if kind == "event" && v.at == nil {
		return v, invalid("an event happens at a time: give at")
	}
	if d.Recurrence != nil {
		if v.at == nil {
			return v, invalid("recurrence requires at")
		}
		r := *d.Recurrence
		if e := r.normalize(); e != nil {
			return v, e
		}
		v.rec = &r
	}
	if v.alert != nil {
		if v.at == nil {
			return v, invalid("alert_before_minutes requires at")
		}
		if *v.alert < 0 || *v.alert > 10080 {
			return v, invalid("alert_before_minutes must be 0-10080")
		}
		if v.rec != nil {
			if _, e = recurringAlert(*v.at, *v.alert, *v.rec, loc); e != nil {
				return v, e
			}
		}
	}
	return v, nil
}

// checkGoal refuses a link to a goal that is not there or is archived.
func (u *unit) checkGoal(ctx context.Context, goal string) error {
	if goal == "" {
		return nil
	}
	var found bool
	if e := u.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM life.goals WHERE id=$1 AND deleted_at IS NULL)", goal).Scan(&found); e != nil {
		return e
	}
	if !found {
		return invalid("goal_id points at a goal that does not exist or is archived")
	}
	return nil
}

// sessionFor is the conversation an agent's item wakes: the one it was created from. A person's item needs none.
func (u *unit) sessionFor(v itemValues) (string, error) {
	if v.subject != "agent" {
		return "", nil
	}
	if u.session == "" {
		if v.alert != nil {
			return "", invalid("an agent's item with an alert wakes the conversation it was created from; create it from a conversation")
		}
		return "", nil
	}
	return sessionScope(u.session), nil
}

func jsonOrNil(v any) any {
	if r, ok := v.(*Recurrence); ok && r == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func createItem(kind string) func(context.Context, *unit, CreateItemInput) (Item, error) {
	return func(ctx context.Context, u *unit, in CreateItemInput) (Item, error) {
		key, e := createID(in.ID)
		if e != nil {
			return Item{}, e
		}
		v, e := in.ItemData.values(kind)
		if e != nil {
			return Item{}, e
		}
		if e = u.checkGoal(ctx, v.goal); e != nil {
			return Item{}, e
		}
		session, e := u.sessionFor(v)
		if e != nil {
			return Item{}, e
		}
		_, e = u.tx.Exec(ctx, `INSERT INTO life.items(id,kind,subject,title,notes,goal_id,at,minutes,time_zone,recurrence,alert_before_minutes,is_priority,session_key)
			VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,NULLIF($13,''))`,
			key, kind, v.subject, v.title, v.notes, v.goal, v.at, v.minutes, v.tz, jsonOrNil(v.rec), v.alert, v.priority, session)
		if e != nil {
			return Item{}, e
		}
		it, e := u.getItem(ctx, kind, key, false)
		if e != nil {
			return it, e
		}
		return it, u.audit(ctx, "item", key, nil, it)
	}
}

func updateItem(kind string) func(context.Context, *unit, UpdateItemInput) (Item, error) {
	return func(ctx context.Context, u *unit, in UpdateItemInput) (Item, error) {
		old, e := u.getItem(ctx, kind, in.ID, true)
		if e != nil {
			return old, e
		}
		if e = version(in.ExpectedVersion, old.Version); e != nil {
			return old, e
		}
		if old.DeletedAt != nil {
			return old, conflict("Restore it before editing it")
		}
		v, e := in.ItemData.values(kind)
		if e != nil {
			return old, e
		}
		if v.goal != "" && (old.GoalID == nil || *old.GoalID != v.goal) {
			if e = u.checkGoal(ctx, v.goal); e != nil {
				return old, e
			}
		}
		session, e := u.sessionFor(v)
		if e != nil {
			return old, e
		}
		if session == "" {
			session = old.SessionKey
		}
		// Editing the time starts a fresh occurrence, so a snooze no longer applies.
		_, e = u.tx.Exec(ctx, `UPDATE life.items SET subject=$2,title=$3,notes=$4,goal_id=NULLIF($5,'')::uuid,at=$6,minutes=$7,time_zone=$8,recurrence=$9,
			alert_before_minutes=$10,is_priority=$11,session_key=NULLIF($12,''),snoozed_until=NULL,version=version+1,updated_at=now() WHERE id=$1`,
			old.ID, v.subject, v.title, v.notes, v.goal, v.at, v.minutes, v.tz, jsonOrNil(v.rec), v.alert, v.priority, session)
		if e != nil {
			return old, e
		}
		return u.itemChanged(ctx, kind, old)
	}
}

// itemChanged re-reads a mutated item and records the audit entry.
func (u *unit) itemChanged(ctx context.Context, kind string, old Item) (Item, error) {
	it, e := u.getItem(ctx, kind, old.ID, false)
	if e != nil {
		return it, e
	}
	return it, u.audit(ctx, "item", it.ID, old, it)
}

// completeTask finishes a one-off task, or one occurrence of a repeating one. A repeating task carries on.
func completeTask(ctx context.Context, u *unit, in CompleteItemInput) (Item, error) {
	old, e := u.getItem(ctx, "task", in.ID, true)
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
		if _, e = u.tx.Exec(ctx, `UPDATE life.items SET done_at=$2,snoozed_until=NULL,version=version+1,updated_at=now() WHERE id=$1`, old.ID, now); e != nil {
			return old, e
		}
		if in.Result != "" && old.At != nil {
			if _, e = u.tx.Exec(ctx, `INSERT INTO life.occurrences(item_id,at,state,result) VALUES($1,$2,'done',$3) ON CONFLICT DO NOTHING`, old.ID, old.At.Truncate(time.Second), in.Result); e != nil {
				return old, e
			}
		}
		return u.itemChanged(ctx, "task", old)
	}
	// A repeating task: the occurrence named, else the latest one that has come due, else the next.
	var at time.Time
	if in.At != nil {
		if at, e = instant("at", *in.At); e != nil {
			return old, e
		}
	} else {
		loc, _ := zone(old.TimeZone)
		at = latestOccurrenceAtOrBefore(*old.At, now, *old.Recurrence, loc)
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.occurrences(item_id,at,state,result) VALUES($1,$2,'done',$3)
		ON CONFLICT (item_id, at) DO UPDATE SET state='done', result=EXCLUDED.result, acted_at=now()`, old.ID, at.UTC().Truncate(time.Second), in.Result); e != nil {
		return old, e
	}
	return old, u.audit(ctx, "item", old.ID, nil, map[string]any{"occurrence": at.UTC(), "state": "done", "result": in.Result})
}

// reopenTask undoes a completion: a one-off is open again, a repeating task's named occurrence (default the one now due) is
// no longer done.
func reopenTask(ctx context.Context, u *unit, old Item, in CompleteItemInput, now time.Time) (Item, error) {
	if old.Recurrence == nil {
		if old.DoneAt == nil {
			return old, conflict("Task is not done")
		}
		if _, e := u.tx.Exec(ctx, `UPDATE life.items SET done_at=NULL,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
			return old, e
		}
		return u.itemChanged(ctx, "task", old)
	}
	var at time.Time
	if in.At != nil {
		var e error
		if at, e = instant("at", *in.At); e != nil {
			return old, e
		}
	} else {
		loc, _ := zone(old.TimeZone)
		at = latestOccurrenceAtOrBefore(*old.At, now, *old.Recurrence, loc)
	}
	tag, e := u.tx.Exec(ctx, `DELETE FROM life.occurrences WHERE item_id=$1 AND at=$2`, old.ID, at.UTC().Truncate(time.Second))
	if e != nil {
		return old, e
	}
	if tag.RowsAffected() == 0 {
		return old, conflict("That occurrence is not done")
	}
	return old, u.audit(ctx, "item", old.ID, nil, map[string]any{"occurrence": at.UTC(), "state": "reopened"})
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

func snoozeItem(kind string) func(context.Context, *unit, SnoozeItemInput) (Item, error) {
	return func(ctx context.Context, u *unit, in SnoozeItemInput) (Item, error) {
		old, e := u.getItem(ctx, kind, in.ID, true)
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
			return old, conflict("Only an open item can be snoozed")
		}
		if old.AlertBeforeMinutes == nil {
			return old, invalid("This item has no alert to snooze")
		}
		if _, e = u.tx.Exec(ctx, `UPDATE life.items SET snoozed_until=$2,version=version+1,updated_at=now() WHERE id=$1`, old.ID, until); e != nil {
			return old, e
		}
		return u.itemChanged(ctx, kind, old)
	}
}

func lifecycleItem(kind string, restore bool) func(context.Context, *unit, EditInput) (Item, error) {
	return func(ctx context.Context, u *unit, in EditInput) (Item, error) {
		old, e := u.getItem(ctx, kind, in.ID, true)
		if e != nil {
			return old, e
		}
		if e = version(in.ExpectedVersion, old.Version); e != nil {
			return old, e
		}
		if restore == (old.DeletedAt == nil) {
			if restore {
				return old, conflict("It is not deleted")
			}
			return old, conflict("It is already deleted")
		}
		set := "deleted_at=now()"
		if restore {
			set = "deleted_at=NULL"
		}
		if _, e = u.tx.Exec(ctx, `UPDATE life.items SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
			return old, e
		}
		return u.itemChanged(ctx, kind, old)
	}
}

func listItems(kind string) func(context.Context, *unit, ListItemsInput) (Page[Item], error) {
	return func(ctx context.Context, u *unit, in ListItemsInput) (Page[Item], error) {
		result := Page[Item]{Items: []Item{}}
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
		where, args := []string{"kind=$1", "subject=$2"}, []any{kind, subject}
		arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
		switch state {
		case "active":
			where = append(where, "deleted_at IS NULL")
		case "deleted":
			where = append(where, "deleted_at IS NOT NULL")
		}
		if kind == "task" && status != "all" {
			// A repeating task is never done as a whole; it stays open.
			if status == "open" {
				where = append(where, "(done_at IS NULL)")
			} else {
				where = append(where, "(done_at IS NOT NULL)")
			}
		}
		if in.GoalID != "" {
			g, e := id(in.GoalID)
			if e != nil {
				return result, invalid("goal_id must be a UUID")
			}
			where = append(where, "goal_id="+arg(g))
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
		if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.items WHERE "+clause, args...).Scan(&result.Total); e != nil {
			return result, e
		}
		rows, e := u.tx.Query(ctx, "SELECT "+itemCols+" FROM life.items WHERE "+clause+" ORDER BY at ASC NULLS LAST, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
		if e != nil {
			return result, e
		}
		defer rows.Close()
		for rows.Next() {
			it, e := scanItem(rows)
			if e != nil {
				return result, e
			}
			result.Items = append(result.Items, it)
		}
		result.NextOffset = nextOffset(offset, limit, result.Total)
		return result, rows.Err()
	}
}
