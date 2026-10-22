package engine

import (
	"context"
)

// The delivery contract: the two operations every engine implements so mcp-hub can collect what it noticed without
// understanding any of it (agent-harness docs/components/event-delivery.md). Nothing here reaches outwards: the engine
// records and answers when asked, and delivery, retries and waking are the hub's job.
//
// Pending notices are the only thing in this feed: an alert coming due for a person's item is recorded as a notice by Fire
// (alerts.go), as is anything the agent tells the person (notices.go). There is no engine timer here; latency is the hub's
// delivery interval.

// linkEntities are the record kinds a notice can open.
var linkEntities = []string{"goal", "task", "event"}

func (u *unit) eventsPending(ctx context.Context) (EventsResult, error) {
	result := EventsResult{Items: []Event{}}
	items, err := u.pendingNotices(ctx)
	if err != nil {
		return result, err
	}
	result.Items = append(result.Items, items...)
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
