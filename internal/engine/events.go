package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The delivery contract: the two operations every engine implements so mcp-hub can collect what it noticed without
// understanding any of it (agent-harness docs/components/event-delivery.md). Nothing here reaches outwards: the engine
// records and answers when asked, and delivery, retries and waking are the hub's job.
//
// Pending notices are derived from records, not stored: a pending reminder whose effective due time has passed and
// whose occurrence has not been acknowledged. There is no engine timer; latency is the hub's delivery interval.

const reminderEventKey = "life:reminder"

// effectiveDue is the instant a pending reminder next needs attention.
const effectiveDue = `coalesce(r.snoozed_until, r.due_at)`

// reminderEventID names one occurrence: the reminder and the whole second it was due, so a redelivery is recognisable and an edit of the due time is a new occurrence.
func reminderEventID(id string, due time.Time) string {
	return "reminder:" + id + ":" + strconv.FormatInt(due.Unix(), 10)
}

func (u *unit) pendingReminders(ctx context.Context) ([]Event, error) {
	var items []Event
	rows, err := u.tx.Query(ctx, `SELECT r.id::text,r.title,r.notes,`+effectiveDue+`
		FROM life.reminders r
		WHERE r.deleted_at IS NULL AND r.state='pending' AND `+effectiveDue+` <= now()
		  AND NOT EXISTS (SELECT 1 FROM life.acked_events a
		       WHERE a.event_id = 'reminder:' || r.id::text || ':' || floor(extract(epoch FROM `+effectiveDue+`))::bigint::text)
		ORDER BY `+effectiveDue+`, r.id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rid, title, notes string
		var due time.Time
		if err := rows.Scan(&rid, &title, &notes, &due); err != nil {
			return nil, err
		}
		body := notes
		if body == "" {
			body = "Due now"
		}
		items = append(items, Event{
			// A class key, never per record: subscriptions are registered against the key, and a per-record key could never be subscribed to.
			EventKey: reminderEventKey,
			EventID:  reminderEventID(rid, due),
			Detail:   fmt.Sprintf("The reminder %q is due.", title),
			Notice:   Notice{Kind: "reminder.due", Title: title, Body: body, Priority: "normal", Link: &Link{Entity: "reminder", ID: rid}},
		})
	}
	return items, rows.Err()
}

func (u *unit) eventsPending(ctx context.Context) (EventsResult, error) {
	result := EventsResult{Items: []Event{}}
	for _, source := range []func(context.Context) ([]Event, error){u.pendingReminders, u.pendingSchedules} {
		items, err := source(ctx)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, items...)
	}
	if len(result.Items) > 100 {
		result.Items = result.Items[:100]
	}
	return result, nil
}

// eventsAck marks occurrences handed over. Idempotent by construction. A repeating reminder moves on to its next
// occurrence here, because it has now fired; a one-off stays pending (overdue) until the person completes it.
func (u *unit) eventsAck(ctx context.Context, in AckEventsInput) (AckEventsResult, error) {
	result := AckEventsResult{}
	if len(in.EventIDs) > 500 {
		return result, invalid("at most 500 event_ids per ack")
	}
	now, err := u.now(ctx)
	if err != nil {
		return result, err
	}
	for _, eventID := range in.EventIDs {
		if len(eventID) > 200 {
			return result, invalid("event_id is too long")
		}
		tag, err := u.tx.Exec(ctx, `INSERT INTO life.acked_events(event_id) VALUES($1) ON CONFLICT DO NOTHING`, eventID)
		if err != nil {
			return result, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		result.Acknowledged++
		parts := strings.Split(eventID, ":")
		if len(parts) != 3 {
			continue
		}
		epoch, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		switch parts[0] {
		case "reminder":
			err = u.advanceFired(ctx, parts[1], epoch, now)
		case "schedule":
			err = u.advanceSchedule(ctx, parts[1], epoch, now)
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (u *unit) advanceFired(ctx context.Context, rid string, epoch int64, now time.Time) error {
	r, err := u.getReminder(ctx, rid, true)
	if err != nil {
		return nil // the reminder is gone; nothing to advance
	}
	if r.Recurrence == nil || r.DeletedAt != nil || r.State != "pending" || r.DueAt == nil {
		return nil
	}
	due := *r.DueAt
	if r.SnoozedUntil != nil {
		due = *r.SnoozedUntil
	}
	if due.Unix() != epoch {
		return nil // edited since this notice was raised
	}
	loc, _ := zone(r.TimeZone)
	next := nextOccurrence(*r.DueAt, now, *r.Recurrence, loc)
	if _, err := u.tx.Exec(ctx, `UPDATE life.reminders SET due_at=$2,snoozed_until=NULL,version=version+1,updated_at=now() WHERE id=$1`, rid, next); err != nil {
		return err
	}
	_, err = u.afterChange(ctx, r)
	return err
}

// pendingSchedules raises one notice per fired occurrence of an enabled schedule. The default route wakes the agent
// with the person's instruction; the notice lets a subscription deliver "time for your weekly review" directly instead.
func (u *unit) pendingSchedules(ctx context.Context) ([]Event, error) {
	var items []Event
	rows, err := u.tx.Query(ctx, `SELECT s.id::text,s.name,s.instruction,s.next_at FROM life.schedules s
		WHERE s.deleted_at IS NULL AND s.enabled AND s.next_at <= now()
		  AND NOT EXISTS (SELECT 1 FROM life.acked_events a WHERE a.event_id = 'schedule:' || s.id::text || ':' || floor(extract(epoch FROM s.next_at))::bigint::text)
		ORDER BY s.next_at, s.id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, name, instruction string
		var at time.Time
		if err := rows.Scan(&sid, &name, &instruction, &at); err != nil {
			return nil, err
		}
		items = append(items, Event{
			EventKey: scheduleEventKey,
			EventID:  "schedule:" + sid + ":" + strconv.FormatInt(at.Unix(), 10),
			Detail:   fmt.Sprintf("The person's scheduled prompt %q is due: %s", name, instruction),
			Notice:   Notice{Kind: "schedule.due", Title: name, Body: instruction, Priority: "normal", Link: &Link{Entity: "schedule", ID: sid}},
		})
	}
	return items, rows.Err()
}

const scheduleEventKey = "life:schedule"

// advanceSchedule moves a fired schedule on to its next occurrence.
func (u *unit) advanceSchedule(ctx context.Context, sid string, epoch int64, now time.Time) error {
	s, err := u.getSchedule(ctx, sid, true)
	if err != nil || s.DeletedAt != nil || !s.Enabled || s.NextAt.Unix() != epoch {
		return nil // gone, disabled, or edited since this notice was raised
	}
	loc, _ := zone(s.TimeZone)
	next := nextOccurrence(s.NextAt, now, s.Recurrence, loc).UTC()
	if _, err := u.tx.Exec(ctx, `UPDATE life.schedules SET next_at=$2,last_fired_at=$3,version=version+1,updated_at=now() WHERE id=$1`, sid, next, now); err != nil {
		return err
	}
	_, err = u.scheduleChanged(ctx, s)
	return err
}
