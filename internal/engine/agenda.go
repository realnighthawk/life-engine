package engine

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// A reminder is a cue. The agenda lists a day's cues, one entry per occurrence, so a repeating reminder shows as many times as
// it falls that day, and each is cleared on its own (reminders_ack). The day's progress counts a cue once it has fired.
type AgendaInput struct {
	Day string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
}
type AgendaItem struct {
	Reminder Reminder  `json:"reminder"`
	At       time.Time `json:"at" jsonschema:"when this occurrence is due"`
	State    string    `json:"state" jsonschema:"upcoming; due (it has fired and not yet been cleared); or acknowledged"`
	Overdue  bool      `json:"overdue" jsonschema:"an open one-off from an earlier day, carried onto today"`
}
type Agenda struct {
	Day   string       `json:"day"`
	Items []AgendaItem `json:"items" jsonschema:"soonest first"`
}
type AckReminderInput struct {
	Meta
	ID string  `json:"id"`
	At *string `json:"at,omitempty" jsonschema:"RFC 3339 occurrence being cleared; default the reminder's current due time"`
}

// occurrencesIn expands a repeating reminder's series between start (inclusive) and end (exclusive).
func occurrencesIn(anchor time.Time, rule Recurrence, loc *time.Location, start, end time.Time) []time.Time {
	var out []time.Time
	if !anchor.Before(start) && anchor.Before(end) {
		out = append(out, anchor)
	}
	after := start.Add(-time.Second)
	if after.Before(anchor) {
		after = anchor
	}
	t := after
	for i := 0; i < 64; i++ { // a day holds a handful of occurrences at most; the bound only guards a malformed rule
		t = nextOccurrence(anchor, t, rule, loc)
		if !t.Before(end) {
			break
		}
		if !t.Before(start) {
			out = append(out, t)
		}
	}
	return out
}

func remindersAgenda(ctx context.Context, u *unit, in AgendaInput) (Agenda, error) {
	today, loc, now, e := u.today(ctx)
	d := today
	if in.Day != "" {
		if d, e = day("day", in.Day); e != nil {
			return Agenda{}, e
		}
	}
	if e != nil {
		return Agenda{}, e
	}
	start, _ := time.ParseInLocation("2006-01-02", d, loc)
	end := start.AddDate(0, 0, 1)
	out := Agenda{Day: d, Items: []AgendaItem{}}

	rows, e := u.tx.Query(ctx, "SELECT "+reminderCols+" FROM life.reminders WHERE deleted_at IS NULL AND (state='pending' OR (due_at >= $1 AND due_at < $2) OR (completed_at >= $1 AND completed_at < $2)) ORDER BY due_at NULLS LAST, id", start, end)
	if e != nil {
		return out, e
	}
	var reminders []Reminder
	for rows.Next() {
		r, e := scanReminder(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		reminders = append(reminders, r)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	anchors := map[string]time.Time{}
	arows, e := u.tx.Query(ctx, `SELECT id::text, series_anchor FROM life.reminders WHERE series_anchor IS NOT NULL AND deleted_at IS NULL`)
	if e != nil {
		return out, e
	}
	for arows.Next() {
		var id string
		var a time.Time
		if e = arows.Scan(&id, &a); e != nil {
			arows.Close()
			return out, e
		}
		anchors[id] = a
	}
	arows.Close()
	acked := map[string]bool{}
	krows, e := u.tx.Query(ctx, `SELECT reminder_id::text, occurrence_at FROM life.reminder_acks WHERE occurrence_at >= $1 AND occurrence_at < $2`, start, end)
	if e != nil {
		return out, e
	}
	for krows.Next() {
		var id string
		var at time.Time
		if e = krows.Scan(&id, &at); e != nil {
			krows.Close()
			return out, e
		}
		acked[id+"|"+strconv.FormatInt(at.Unix(), 10)] = true
	}
	krows.Close()

	for _, r := range reminders {
		var times []time.Time
		overdue := false
		switch {
		case r.Recurrence != nil && anchors[r.ID] != (time.Time{}):
			rloc, _ := zone(r.TimeZone)
			for _, t := range occurrencesIn(anchors[r.ID].UTC(), *r.Recurrence, rloc, start, end) {
				if r.SnoozedUntil != nil && r.DueAt != nil && t.Equal(*r.DueAt) {
					continue // this occurrence was snoozed; it is listed at its new time instead
				}
				times = append(times, t)
			}
			if r.SnoozedUntil != nil && !r.SnoozedUntil.Before(start) && r.SnoozedUntil.Before(end) {
				times = append(times, *r.SnoozedUntil)
			}
		case r.DueAt != nil:
			at := *r.DueAt
			if r.SnoozedUntil != nil {
				at = *r.SnoozedUntil
			}
			if !at.Before(start) && at.Before(end) {
				times = append(times, at)
			} else if d == today && r.State == "pending" && at.Before(start) {
				times, overdue = append(times, at), true
			}
		}
		for _, at := range times {
			state := "upcoming"
			switch {
			case acked[r.ID+"|"+strconv.FormatInt(at.Unix(), 10)] || (r.Recurrence == nil && r.State == "done"):
				state = "acknowledged"
			case !at.After(now):
				state = "due"
			}
			out.Items = append(out.Items, AgendaItem{Reminder: r, At: at.UTC(), State: state, Overdue: overdue})
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].At.Before(out.Items[j].At) })
	return out, nil
}

// ackReminder clears one occurrence. A one-off is finished by it; a repeating reminder carries on, and only that occurrence is
// cleared. It is idempotent per occurrence.
func ackReminder(ctx context.Context, u *unit, in AckReminderInput) (Reminder, error) {
	r, e := u.getReminder(ctx, in.ID, true)
	if e != nil {
		return r, e
	}
	if r.DeletedAt != nil {
		return r, conflict("Restore the reminder before clearing it")
	}
	var at time.Time
	switch {
	case in.At != nil:
		if at, e = instant("at", *in.At); e != nil {
			return r, e
		}
	case r.SnoozedUntil != nil:
		at = *r.SnoozedUntil
	case r.DueAt != nil:
		at = *r.DueAt
	default:
		return r, invalid("This reminder has no time; finish it with reminders_complete")
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.reminder_acks(reminder_id, occurrence_at) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.ID, at.UTC().Truncate(time.Second)); e != nil {
		return r, e
	}
	switch {
	case r.Recurrence == nil && r.State == "pending":
		_, e = u.tx.Exec(ctx, `UPDATE life.reminders SET state='done',completed_at=now(),last_completed_at=now(),snoozed_until=NULL,version=version+1,updated_at=now() WHERE id=$1`, r.ID)
		if e != nil {
			return r, e
		}
		return u.afterChange(ctx, r)
	case r.Recurrence != nil:
		// The series carries on; remember when it was last cleared without making the reminder look edited.
		_, e = u.tx.Exec(ctx, `UPDATE life.reminders SET last_completed_at=now() WHERE id=$1`, r.ID)
		if e != nil {
			return r, e
		}
	}
	return u.getReminder(ctx, r.ID, false)
}
