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
// Pending notices are derived from records, not stored: a schedule or routine occurrence that has come due and
// has not been acknowledged. There is no engine timer; latency is the hub's delivery interval.

// Reminders are not here: they fire from a Temporal Schedule armed by SyncWakes (wake.go, ADR 0003), not from this feed.

func (u *unit) eventsPending(ctx context.Context) (EventsResult, error) {
	result := EventsResult{Items: []Event{}}
	for _, source := range []func(context.Context) ([]Event, error){u.pendingSchedules, u.pendingRoutines, u.pendingCaptures} {
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

// eventsAck marks occurrences handed over. Idempotent by construction. A repeating schedule moves on to its next
// occurrence here, because it has now fired.
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
		case "schedule":
			err = u.advanceSchedule(ctx, parts[1], epoch, now)
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
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

const (
	routineEventKey    = "life:routine"
	routineGapEventKey = "life:routine_gap"
)

// pendingRoutines raises two kinds of notice, both derived from the routine and its check-ins:
//   - a nudge at the person's chosen time on a day the routine applies and is not yet done;
//   - one notice when a gap reaches three days, so an easy restart can be offered.
//
// Each occurrence has a deterministic id (the routine and the local day, or the last check-in day), so acknowledging it
// is the whole of the delivery state.
func (u *unit) pendingRoutines(ctx context.Context) ([]Event, error) {
	var items []Event
	rows, err := u.tx.Query(ctx, "SELECT "+routineCols+" FROM life.routines WHERE deleted_at IS NULL ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	var routines []Routine
	for rows.Next() {
		r, err := scanRoutine(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		routines = append(routines, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now, err := u.now(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range routines {
		loc, e := zone(r.TimeZone)
		if e != nil {
			continue
		}
		local := now.In(loc)
		d := local.Format("2006-01-02")
		st, err := u.routineStatus(ctx, r, d)
		if err != nil {
			return nil, err
		}
		if r.RemindAt != nil && st.Scheduled && !st.Done && local.Format("15:04") >= *r.RemindAt {
			acked, err := u.acked(ctx, routineEventID("routine", r.ID, toDayNumber(d)))
			if err != nil {
				return nil, err
			}
			if !acked {
				items = append(items, Event{EventKey: routineEventKey, EventID: routineEventID("routine", r.ID, toDayNumber(d)),
					Detail: fmt.Sprintf("The routine %q is due today and not done.", r.Title),
					Notice: Notice{Kind: "routine.due", Title: r.Title, Body: fmt.Sprintf("%d min, or the short version: %s (%d min)", r.Minutes, r.ShortTitle, r.ShortMinutes), Priority: "normal", Link: &Link{Entity: "routine", ID: r.ID}}})
			}
		}
		if st.Gap {
			anchor := toDayNumber(r.CreatedAt.In(loc).Format("2006-01-02"))
			if st.DaysSinceLast != nil {
				anchor = toDayNumber(d) - dayNumber(*st.DaysSinceLast)
			}
			eid := routineEventID("routine_gap", r.ID, anchor)
			acked, err := u.acked(ctx, eid)
			if err != nil {
				return nil, err
			}
			if !acked {
				items = append(items, Event{EventKey: routineGapEventKey, EventID: eid,
					Detail: fmt.Sprintf("The person has not done the routine %q for a few days. Offer an easy restart.", r.Title),
					Notice: Notice{Kind: "routine.gap", Title: r.Title, Body: st.Hint, Priority: "normal", Link: &Link{Entity: "routine", ID: r.ID}}})
			}
		}
	}
	return items, nil
}

// routineEventID names an occurrence by the routine and a calendar day: "<kind>:<id>:<epoch of that day's midnight UTC>".
func routineEventID(kind, rid string, d dayNumber) string {
	return kind + ":" + rid + ":" + strconv.FormatInt(int64(d)*86400, 10)
}

func (u *unit) acked(ctx context.Context, eventID string) (bool, error) {
	var found bool
	err := u.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM life.acked_events WHERE event_id=$1)`, eventID).Scan(&found)
	return found, err
}
