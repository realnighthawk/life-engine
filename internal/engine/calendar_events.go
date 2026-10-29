package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// An event happens at its time whether or not anyone acts; the person shows up. It is not completable: it is cancelled
// or it occurs. A time is required, and repeat or an alert is optional.
type CalendarEvent struct {
	ID                 string          `json:"id"`
	Subject            string          `json:"subject" jsonschema:"person (the default) or agent"`
	Title              string          `json:"title"`
	Notes              string          `json:"notes"`
	At                 time.Time       `json:"at" jsonschema:"when it starts"`
	Minutes            *int            `json:"minutes" jsonschema:"how long it lasts, or null"`
	TimeZone           string          `json:"time_zone"`
	Recurrence         *Recurrence     `json:"recurrence" jsonschema:"how it repeats, or null for a one-off"`
	AlertBeforeMinutes *int            `json:"alert_before_minutes" jsonschema:"tell the owner this many minutes before at, or null"`
	SessionKey         string          `json:"session_key,omitempty" jsonschema:"for an agent's event, the conversation its alert wakes"`
	SnoozedUntil       *time.Time      `json:"snoozed_until"`
	CancelledAt        *time.Time      `json:"cancelled_at" jsonschema:"when it was cancelled, or null"`
	Origin             string          `json:"origin" jsonschema:"person or agent: who authored it"`
	DerivedFrom        json.RawMessage `json:"derived_from,omitempty"`
	Version            int64           `json:"version"`
	DeletedAt          *time.Time      `json:"deleted_at"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type EventData struct {
	TimingData
	Title string `json:"title" jsonschema:"1-200 characters"`
	Notes string `json:"notes,omitempty" jsonschema:"short detail, up to 2000 characters"`
}
type CreateEventInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	EventData
}
type UpdateEventInput struct {
	EditInput
	EventData
}
type SnoozeEventInput struct {
	EditInput
	Until string `json:"until" jsonschema:"RFC 3339 instant in the future the alert is deferred to"`
}
type ListEventsInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Status  string `json:"status,omitempty" jsonschema:"upcoming (default), past, cancelled or all"`
	After   string `json:"after,omitempty" jsonschema:"RFC 3339; only events at or after this time"`
	Before  string `json:"before,omitempty" jsonschema:"RFC 3339; only events before this time"`
}

const eventCols = `id::text,subject,title,notes,at,minutes,time_zone,recurrence,alert_before_minutes,coalesce(session_key,''),snoozed_until,cancelled_at,origin,derived_from,version,deleted_at,created_at,updated_at`

func scanEvent(row scanner) (CalendarEvent, error) {
	var e CalendarEvent
	var rec, derived []byte
	err := row.Scan(&e.ID, &e.Subject, &e.Title, &e.Notes, &e.At, &e.Minutes, &e.TimeZone, &rec, &e.AlertBeforeMinutes,
		&e.SessionKey, &e.SnoozedUntil, &e.CancelledAt, &e.Origin, &derived, &e.Version, &e.DeletedAt, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	if len(rec) > 0 {
		e.Recurrence = new(Recurrence)
		if err = json.Unmarshal(rec, e.Recurrence); err != nil {
			return e, err
		}
	}
	if len(derived) > 0 {
		e.DerivedFrom = json.RawMessage(derived)
	}
	for _, p := range []*time.Time{e.SnoozedUntil, e.CancelledAt, e.DeletedAt} {
		utcPtr(p)
	}
	e.At = e.At.UTC()
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	return e, nil
}

func (u *unit) getEvent(ctx context.Context, key string, forUpdate bool) (CalendarEvent, error) {
	k, e := u.resolveRef(ctx, "events", "", key)
	if e != nil {
		return CalendarEvent{}, e
	}
	q := "SELECT " + eventCols + " FROM life.events WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	ev, e := scanEvent(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "events", "", k)
	}
	return ev, e
}

func createEvent(ctx context.Context, u *unit, in CreateEventInput) (CalendarEvent, error) {
	key, e := createID(in.ID)
	if e != nil {
		return CalendarEvent{}, e
	}
	v, e := in.TimingData.values(true)
	if e != nil {
		return CalendarEvent{}, e
	}
	title, notes, e := trimTitleNotes(in.Title, in.Notes)
	if e != nil {
		return CalendarEvent{}, e
	}
	session, e := u.sessionFor(v)
	if e != nil {
		return CalendarEvent{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.events(id,subject,title,notes,at,minutes,time_zone,recurrence,alert_before_minutes,
		session_key,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12)`,
		key, v.subject, title, notes, v.at, v.minutes, v.tz, jsonOrNil(v.rec), v.alert, session, v.origin, v.derived); e != nil {
		return CalendarEvent{}, e
	}
	ev, e := u.getEvent(ctx, key, false)
	if e != nil {
		return ev, e
	}
	return ev, u.audit(ctx, "event", key, nil, ev)
}

func updateEvent(ctx context.Context, u *unit, in UpdateEventInput) (CalendarEvent, error) {
	old, e := u.getEvent(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the event before editing it")
	}
	v, e := in.TimingData.values(true)
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
	if _, e = u.tx.Exec(ctx, `UPDATE life.events SET subject=$2,title=$3,notes=$4,at=$5,minutes=$6,time_zone=$7,recurrence=$8,
		alert_before_minutes=$9,session_key=NULLIF($10,''),origin=$11,derived_from=$12,snoozed_until=NULL,version=version+1,updated_at=now()
		WHERE id=$1`, old.ID, v.subject, title, notes, v.at, v.minutes, v.tz, jsonOrNil(v.rec), v.alert, session, v.origin, v.derived); e != nil {
		return old, e
	}
	return u.eventChanged(ctx, old)
}

func (u *unit) eventChanged(ctx context.Context, old CalendarEvent) (CalendarEvent, error) {
	ev, e := u.getEvent(ctx, old.ID, false)
	if e != nil {
		return ev, e
	}
	return ev, u.audit(ctx, "event", ev.ID, old, ev)
}

func snoozeEvent(ctx context.Context, u *unit, in SnoozeEventInput) (CalendarEvent, error) {
	old, e := u.getEvent(ctx, in.ID, true)
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
	if old.DeletedAt != nil || old.CancelledAt != nil {
		return old, conflict("Only an active event can be snoozed")
	}
	if old.AlertBeforeMinutes == nil {
		return old, invalid("This event has no alert to snooze")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.events SET snoozed_until=$2,version=version+1,updated_at=now() WHERE id=$1`, old.ID, until); e != nil {
		return old, e
	}
	return u.eventChanged(ctx, old)
}

// setEventCancelled marks an event cancelled or brings it back. It is not a delete: the record and its history stay.
func setEventCancelled(ctx context.Context, u *unit, in EditInput, cancelled bool) (CalendarEvent, error) {
	old, e := u.getEvent(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if cancelled == (old.CancelledAt != nil) {
		if cancelled {
			return old, conflict("CalendarEvent is already cancelled")
		}
		return old, conflict("CalendarEvent is not cancelled")
	}
	set := "cancelled_at=now()"
	if !cancelled {
		set = "cancelled_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.events SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.eventChanged(ctx, old)
}

func lifecycleEvent(ctx context.Context, u *unit, in EditInput, restore bool) (CalendarEvent, error) {
	old, e := u.getEvent(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("CalendarEvent is not deleted")
		}
		return old, conflict("CalendarEvent is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.events SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.eventChanged(ctx, old)
}

func listEvents(ctx context.Context, u *unit, in ListEventsInput) (Page[CalendarEvent], error) {
	result := Page[CalendarEvent]{Items: []CalendarEvent{}}
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
		status = "upcoming"
	}
	if !oneOf(status, "upcoming", "past", "cancelled", "all") {
		return result, invalid("status must be upcoming, past, cancelled or all")
	}
	where, args := []string{"subject=$1"}, []any{subject}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	switch status {
	case "upcoming":
		where = append(where, "cancelled_at IS NULL", "at >= now()")
	case "past":
		where = append(where, "cancelled_at IS NULL", "at < now()")
	case "cancelled":
		where = append(where, "cancelled_at IS NOT NULL")
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
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.events WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+eventCols+" FROM life.events WHERE "+clause+
		" ORDER BY at, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		ev, e := scanEvent(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, ev)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}
