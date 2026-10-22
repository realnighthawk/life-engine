package engine

import (
	"context"
)

// The delivery contract: the two operations every engine implements so mcp-hub can collect what it noticed without
// understanding any of it (agent-harness docs/components/event-delivery.md). Nothing here reaches outwards: the engine
// records and answers when asked, and delivery, retries and waking are the hub's job.
//
// The engine emits no events any more: a person's alert goes straight to the gateway (clock/fire.go) and the agent tells the
// person something with notify_user. The hub still asks every engine for its events, so the operations stay and answer an empty
// list; acknowledging is idempotent by construction.

func (u *unit) eventsPending(ctx context.Context) (EventsResult, error) {
	return EventsResult{Items: []Event{}}, nil
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
