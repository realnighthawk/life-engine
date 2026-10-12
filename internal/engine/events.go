package engine

import (
	"context"
	"fmt"
	"strconv"
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
	for _, source := range []func(context.Context) ([]Event, error){u.pendingRoutines, u.pendingCaptures} {
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

// eventsAck marks occurrences handed over. Idempotent by construction.
func (u *unit) eventsAck(ctx context.Context, in AckEventsInput) (AckEventsResult, error) {
	result := AckEventsResult{}
	if len(in.EventIDs) > 500 {
		return result, invalid("at most 500 event_ids per ack")
	}
	for _, eventID := range in.EventIDs {
		if len(eventID) > 200 {
			return result, invalid("event_id is too long")
		}
		tag, err := u.tx.Exec(ctx, `INSERT INTO life.acked_events(event_id) VALUES($1) ON CONFLICT DO NOTHING`, eventID)
		if err != nil {
			return result, err
		}
		result.Acknowledged += int(tag.RowsAffected())
	}
	return result, nil
}

const (
	routineEventKey    = "life:routine"
	routineGapEventKey = "life:routine_gap"
)

// pendingRoutines raises one notice when a gap reaches three days, so an easy restart can be offered. It is derived from
// the routine and its check-ins and has a deterministic id (the routine and the last check-in day), so acknowledging it
// is the whole of the delivery state. The nudge at the chosen time is a wake (wake.go), not a notice.
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
