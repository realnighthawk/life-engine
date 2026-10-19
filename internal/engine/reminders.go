package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type Reminder struct {
	ID              string      `json:"id"`
	Title           string      `json:"title"`
	Notes           string      `json:"notes"`
	DueAt           *time.Time  `json:"due_at" jsonschema:"when it is due; null for an undated reminder"`
	SnoozedUntil    *time.Time  `json:"snoozed_until"`
	TimeZone        string      `json:"time_zone"`
	Recurrence      *Recurrence `json:"recurrence"`
	Source          string      `json:"source" jsonschema:"user or agent"`
	SessionID       string      `json:"session_id,omitempty" jsonschema:"the conversation that created it, when the agent did"`
	TurnSeq         *int64      `json:"turn_seq"`
	Link            *Link       `json:"link"`
	State           string      `json:"state" jsonschema:"pending or done"`
	CompletedAt     *time.Time  `json:"completed_at"`
	LastCompletedAt *time.Time  `json:"last_completed_at" jsonschema:"for recurring reminders, the last time one occurrence was completed"`
	Version         int64       `json:"version"`
	DeletedAt       *time.Time  `json:"deleted_at"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type ReminderData struct {
	Title      string      `json:"title" jsonschema:"what to be reminded of, 1-200 characters"`
	Notes      string      `json:"notes,omitempty" jsonschema:"optional detail, up to 2000 characters"`
	DueAt      *string     `json:"due_at,omitempty" jsonschema:"RFC 3339 instant it is due; omit for an undated reminder"`
	TimeZone   string      `json:"time_zone,omitempty" jsonschema:"IANA zone the person lives in (for example Europe/London); recurrence repeats on this zone's wall-clock time. Default UTC"`
	Recurrence *Recurrence `json:"recurrence,omitempty" jsonschema:"optional repeat; requires due_at"`
	Source     string      `json:"source,omitempty" jsonschema:"user (default) or agent"`
	SessionID  string      `json:"session_id,omitempty" jsonschema:"set when the agent creates it, so the person can open the conversation"`
	TurnSeq    *int64      `json:"turn_seq,omitempty"`
	Link       *Link       `json:"link,omitempty" jsonschema:"optional record this reminder is about"`
}
type CreateReminderInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a retry under a new key cannot duplicate"`
	ReminderData
}
type UpdateReminderInput struct {
	EditInput
	ReminderData
}
type SnoozeReminderInput struct {
	EditInput
	Until string `json:"until" jsonschema:"RFC 3339 instant in the future to be reminded again"`
}
type ListRemindersInput struct {
	ListInput
	Status    string `json:"status,omitempty" jsonschema:"pending (default), done or all"`
	DueAfter  string `json:"due_after,omitempty" jsonschema:"RFC 3339; only reminders due at or after this"`
	DueBefore string `json:"due_before,omitempty" jsonschema:"RFC 3339; only reminders due before this"`
	// ponytail: exact match on the one link a reminder carries; a goal's reminders linked through its steps or routines are not included.
	LinkEntity string `json:"link_entity,omitempty" jsonschema:"only reminders linked to this kind of record, with link_id"`
	LinkID     string `json:"link_id,omitempty" jsonschema:"only reminders linked to this record, with link_entity"`
}

const reminderCols = `id::text,title,notes,due_at,snoozed_until,time_zone,recurrence,source,coalesce(session_id,''),turn_seq,link,state,completed_at,last_completed_at,version,deleted_at,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanReminder(row scanner) (Reminder, error) {
	var r Reminder
	var rec, link []byte
	err := row.Scan(&r.ID, &r.Title, &r.Notes, &r.DueAt, &r.SnoozedUntil, &r.TimeZone, &rec, &r.Source, &r.SessionID, &r.TurnSeq, &link, &r.State,
		&r.CompletedAt, &r.LastCompletedAt, &r.Version, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	for _, t := range []*time.Time{r.DueAt, r.SnoozedUntil, r.CompletedAt, r.LastCompletedAt, r.DeletedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	if len(rec) > 0 {
		r.Recurrence = new(Recurrence)
		if err = json.Unmarshal(rec, r.Recurrence); err != nil {
			return r, err
		}
	}
	if len(link) > 0 {
		r.Link = new(Link)
		err = json.Unmarshal(link, r.Link)
	}
	return r, err
}

func (u *unit) getReminder(ctx context.Context, key string, forUpdate bool) (Reminder, error) {
	k, e := id(key)
	if e != nil {
		return Reminder{}, e
	}
	q := "SELECT " + reminderCols + " FROM life.reminders WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanReminder(u.tx.QueryRow(ctx, q, k))
}

// normalized turns the caller's fields into validated values ready to store.
type reminderValues struct {
	title, notes, tz, source, session string
	due                               *time.Time
	rec                               *Recurrence
	turn                              *int64
	link                              *Link
}

func (d ReminderData) values() (reminderValues, error) {
	v := reminderValues{title: strings.TrimSpace(d.Title), notes: d.Notes, tz: d.TimeZone, source: d.Source, session: d.SessionID, turn: d.TurnSeq, link: d.Link}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if len(v.notes) > 2000 {
		return v, invalid("notes must be at most 2000 characters")
	}
	if v.tz == "" {
		v.tz = "UTC"
	}
	if _, e := zone(v.tz); e != nil {
		return v, e
	}
	if v.source == "" {
		v.source = "user"
	}
	if !oneOf(v.source, "user", "agent") {
		return v, invalid("source must be user or agent")
	}
	if d.DueAt != nil {
		t, e := instant("due_at", *d.DueAt)
		if e != nil {
			return v, e
		}
		v.due = &t
	}
	if d.Recurrence != nil {
		if v.due == nil {
			return v, invalid("recurrence requires due_at")
		}
		r := *d.Recurrence
		if e := r.normalize(); e != nil {
			return v, e
		}
		v.rec = &r
	}
	if v.link != nil && (!oneOf(v.link.Entity, linkEntities...) || v.link.ID == "") {
		return v, invalid("link.entity must name a record kind, with an id")
	}
	return v, nil
}

// linkTables maps each linkable record kind to its table.
var linkTables = map[string]string{
	"reminder": "reminders", "schedule": "schedules", "monitor": "monitors", "goal": "goals", "step": "steps",
	"plan_item": "plan_items", "routine": "routines", "journal_entry": "journal_entries", "note": "notes", "capture": "captures",
}

// exists reports whether a record of that kind is there. Inside a batch an earlier step's record counts, because the
// whole plan shares one transaction.
func (u *unit) exists(ctx context.Context, entity, rawID string) (bool, error) {
	key, e := id(rawID)
	if e != nil {
		return false, invalid("id must be a UUID")
	}
	var found bool
	e = u.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM life."+linkTables[entity]+" WHERE id=$1)", key).Scan(&found)
	return found, e
}

// linkExists refuses a link to a record that is not there.
func (u *unit) linkExists(ctx context.Context, l *Link) error {
	if l == nil {
		return nil
	}
	found, e := u.exists(ctx, l.Entity, l.ID)
	if e != nil {
		return e
	}
	if !found {
		return invalid("link points at a " + l.Entity + " that does not exist")
	}
	return nil
}

// linkEntities are the record kinds a reminder or notice can point at.
var linkEntities = []string{"reminder", "schedule", "monitor", "goal", "step", "plan_item", "routine", "journal_entry", "note", "capture"}

func jsonOrNil(v any) any {
	switch x := v.(type) {
	case *Recurrence:
		if x == nil {
			return nil
		}
	case *Link:
		if x == nil {
			return nil
		}
	}
	b, _ := json.Marshal(v)
	return b
}

func createReminder(ctx context.Context, u *unit, in CreateReminderInput) (Reminder, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Reminder{}, e
	}
	v, e := in.ReminderData.values()
	if e != nil {
		return Reminder{}, e
	}
	if e = u.linkExists(ctx, v.link); e != nil {
		return Reminder{}, e
	}
	if v.session == "" {
		v.session = u.session // created by the agent in this session; the wake will land here
	}
	_, e = u.tx.Exec(ctx, `INSERT INTO life.reminders(id,title,notes,due_at,time_zone,recurrence,source,session_id,turn_seq,link,series_anchor)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,CASE WHEN $6::jsonb IS NULL THEN NULL::timestamptz ELSE $4::timestamptz END)`, key, v.title, v.notes, v.due, v.tz, jsonOrNil(v.rec), v.source, v.session, v.turn, jsonOrNil(v.link))
	if e != nil {
		return Reminder{}, e
	}
	r, e := u.getReminder(ctx, key, false)
	if e != nil {
		return r, e
	}
	return r, u.audit(ctx, "reminder", key, nil, r)
}

func updateReminder(ctx context.Context, u *unit, in UpdateReminderInput) (Reminder, error) {
	old, e := u.getReminder(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the reminder before editing it")
	}
	v, e := in.ReminderData.values()
	if e != nil {
		return old, e
	}
	if v.link != nil && (old.Link == nil || *old.Link != *v.link) {
		if e = u.linkExists(ctx, v.link); e != nil {
			return old, e
		}
	}
	// Editing the due time starts a fresh occurrence, so any snooze no longer applies.
	_, e = u.tx.Exec(ctx, `UPDATE life.reminders SET title=$2,notes=$3,due_at=$4,snoozed_until=NULL,time_zone=$5,recurrence=$6,source=$7,session_id=COALESCE(NULLIF($8,''),session_id),turn_seq=$9,link=$10,
		series_anchor=CASE WHEN $6::jsonb IS NULL THEN NULL::timestamptz ELSE $4::timestamptz END,version=version+1,updated_at=now() WHERE id=$1`, old.ID, v.title, v.notes, v.due, v.tz, jsonOrNil(v.rec), v.source, v.session, v.turn, jsonOrNil(v.link))
	if e != nil {
		return old, e
	}
	return u.afterChange(ctx, old)
}

// afterChange re-reads a mutated reminder and records the audit entry.
func (u *unit) afterChange(ctx context.Context, old Reminder) (Reminder, error) {
	r, e := u.getReminder(ctx, old.ID, false)
	if e != nil {
		return r, e
	}
	return r, u.audit(ctx, "reminder", r.ID, old, r)
}

func completeReminder(ctx context.Context, u *unit, in EditInput) (Reminder, error) {
	old, e := u.getReminder(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the reminder before completing it")
	}
	if old.State == "done" {
		return old, conflict("Reminder is already done")
	}
	now, e := u.now(ctx)
	if e != nil {
		return old, e
	}
	if old.Recurrence == nil {
		_, e = u.tx.Exec(ctx, `UPDATE life.reminders SET state='done',completed_at=$2,last_completed_at=$2,snoozed_until=NULL,version=version+1,updated_at=now() WHERE id=$1`, old.ID, now)
	} else {
		// A repeating reminder stays pending: completing this occurrence moves a due one on to the next.
		due := old.DueAt
		if due != nil && !due.After(now) {
			loc, _ := zone(old.TimeZone)
			next := nextOccurrence(*due, now, *old.Recurrence, loc)
			due = &next
		}
		_, e = u.tx.Exec(ctx, `UPDATE life.reminders SET due_at=$2,snoozed_until=NULL,last_completed_at=$3,version=version+1,updated_at=now() WHERE id=$1`, old.ID, due, now)
	}
	if e != nil {
		return old, e
	}
	return u.afterChange(ctx, old)
}

func snoozeReminder(ctx context.Context, u *unit, in SnoozeReminderInput) (Reminder, error) {
	old, e := u.getReminder(ctx, in.ID, true)
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
	if old.DeletedAt != nil || old.State != "pending" {
		return old, conflict("Only a pending reminder can be snoozed")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.reminders SET snoozed_until=$2,version=version+1,updated_at=now() WHERE id=$1`, old.ID, until); e != nil {
		return old, e
	}
	return u.afterChange(ctx, old)
}

func lifecycleReminder(ctx context.Context, u *unit, in EditInput, restore bool) (Reminder, error) {
	old, e := u.getReminder(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Reminder is not deleted")
		}
		return old, conflict("Reminder is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.reminders SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.afterChange(ctx, old)
}

func listReminders(ctx context.Context, u *unit, in ListRemindersInput) (Page[Reminder], error) {
	result := Page[Reminder]{Items: []Reminder{}}
	limit, offset, state, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	status := in.Status
	if status == "" {
		status = "pending"
	}
	if !oneOf(status, "pending", "done", "all") {
		return result, invalid("status must be pending, done or all")
	}
	where, args := []string{"true"}, []any{}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if status != "all" {
		where = append(where, "state="+arg(status))
	}
	if in.Search != "" {
		p := arg(like(in.Search))
		where = append(where, "(title ILIKE "+p+" OR notes ILIKE "+p+")")
	}
	if (in.LinkEntity == "") != (in.LinkID == "") {
		return result, invalid("link_entity and link_id go together")
	}
	if in.LinkEntity != "" {
		if !oneOf(in.LinkEntity, linkEntities...) {
			return result, invalid("link_entity must name a record kind")
		}
		where = append(where, "link->>'entity'="+arg(in.LinkEntity)+" AND link->>'id'="+arg(in.LinkID))
	}
	for _, bound := range []struct{ value, op string }{{in.DueAfter, ">="}, {in.DueBefore, "<"}} {
		if bound.value == "" {
			continue
		}
		t, e := instant("due_after/due_before", bound.value)
		if e != nil {
			return result, e
		}
		where = append(where, "due_at "+bound.op+" "+arg(t))
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.reminders WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+reminderCols+" FROM life.reminders WHERE "+clause+
		" ORDER BY due_at ASC NULLS LAST, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scanReminder(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, r)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}
