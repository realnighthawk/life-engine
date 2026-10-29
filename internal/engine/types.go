package engine

import (
	"encoding/json"
	"time"
)

// Meta is embedded in every keyed write.
type Meta struct {
	IdempotencyKey string `json:"idempotency_key" jsonschema:"1-128 characters, unique per intended write; resend the same key to retry safely"`
}
type GetInput struct {
	ID string `json:"id" jsonschema:"record UUID"`
}
type EditInput struct {
	Meta
	ID              string `json:"id" jsonschema:"record UUID"`
	ExpectedVersion int64  `json:"expected_version" jsonschema:"the version you last read; a stale version is rejected"`
}
type ListInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Offset int    `json:"offset,omitempty" jsonschema:"0-1000000, default 0"`
	State  string `json:"state,omitempty" jsonschema:"active (default), deleted or all"`
	Search string `json:"search,omitempty" jsonschema:"literal text to find"`
}
type Page[T any] struct {
	Items      []T  `json:"items"`
	Total      int  `json:"total"`
	NextOffset *int `json:"next_offset" jsonschema:"offset of the next page, or null at the end"`
}
type Change struct {
	ID         int64           `json:"id"`
	Actor      string          `json:"actor"`
	Operation  string          `json:"operation"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	OccurredAt time.Time       `json:"occurred_at"`
}
type HistoryInput struct {
	ListInput
	EntityType string `json:"entity_type" jsonschema:"intention, task, event, commitment, decision, state, activity, view, measurement or settings"`
	ID         string `json:"id" jsonschema:"record UUID"`
}

// Events: the delivery contract mcp-hub drains. See events.go.
type Link struct {
	Entity string `json:"entity" jsonschema:"intention, task or event"`
	ID     string `json:"id"`
}
type Notice struct {
	Kind     string `json:"kind" jsonschema:"for example capture.open"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Priority string `json:"priority" jsonschema:"normal or high"`
	Link     *Link  `json:"link,omitempty" jsonschema:"the record to open; clients map it to a deep link"`
}
type Event struct {
	EventKey string `json:"event_key" jsonschema:"Class of notice (for example life:capture); subscriptions are registered against it, never per record"`
	EventID  string `json:"event_id" jsonschema:"This occurrence, durable and deterministic so re-delivery is detectable"`
	Detail   string `json:"detail" jsonschema:"What happened, in one sentence, for an agent that is woken about it"`
	Notice   Notice `json:"notice" jsonschema:"What to show the person if delivered directly"`
}
type EventsResult struct {
	Items []Event `json:"items"`
}
type AckEventsInput struct {
	EventIDs []string `json:"event_ids" jsonschema:"The event ids that were handed over. Acks are idempotent."`
}
type AckEventsResult struct {
	Acknowledged int `json:"acknowledged"`
}
