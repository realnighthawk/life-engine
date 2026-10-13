package engine

import (
	"context"
	"strings"
	"time"
)

// A notice is how the agent (or anything else) tells the person something outside the conversation. The engine records it and the
// hub's delivery loop hands it to the gateway like any other event, so it reaches the app this engine belongs to and no other
// (project-hub ADR 0005). Retrying a call with the same id records one notice.

const noticeEventKey = "life:notice"

type NotifyInput struct {
	Meta
	ID       string `json:"id,omitempty" jsonschema:"client-generated UUID, so a retry cannot notify twice"`
	Title    string `json:"title" jsonschema:"what the person sees first, at most 200 characters"`
	Body     string `json:"body,omitempty" jsonschema:"one or two lines of detail, at most 2000 characters"`
	Priority string `json:"priority,omitempty" jsonschema:"normal (default) or high; high is for something time-sensitive that should break quiet hours"`
	Link     *Link  `json:"link,omitempty" jsonschema:"the record to open when the person taps it"`
}

type NotifyResult struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func notifyPerson(ctx context.Context, u *unit, in NotifyInput) (NotifyResult, error) {
	key, e := createID(in.ID)
	if e != nil {
		return NotifyResult{}, e
	}
	title, body := strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	if title == "" || len(title) > 200 {
		return NotifyResult{}, invalid("title must be 1-200 characters")
	}
	if len(body) > 2000 {
		return NotifyResult{}, invalid("body must be at most 2000 characters")
	}
	priority := in.Priority
	if priority == "" {
		priority = "normal"
	}
	if !oneOf(priority, "normal", "high") {
		return NotifyResult{}, invalid("priority must be normal or high")
	}
	var entity, linkID string
	if in.Link != nil {
		if !oneOf(in.Link.Entity, linkEntities...) || strings.TrimSpace(in.Link.ID) == "" || len(in.Link.ID) > 200 {
			return NotifyResult{}, invalid("link needs an entity (" + strings.Join(linkEntities, ", ") + ") and the record's id")
		}
		entity, linkID = in.Link.Entity, strings.TrimSpace(in.Link.ID)
	}
	var created time.Time
	if e = u.tx.QueryRow(ctx, `INSERT INTO life.notices(id,title,body,priority,link_entity,link_id) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''))
		ON CONFLICT (id) DO UPDATE SET id = life.notices.id RETURNING created_at`, key, title, body, priority, entity, linkID).Scan(&created); e != nil {
		return NotifyResult{}, e
	}
	return NotifyResult{ID: key, CreatedAt: created.UTC()}, nil
}

// pendingNotices hands over the notices not yet acknowledged.
func (u *unit) pendingNotices(ctx context.Context) ([]Event, error) {
	rows, err := u.tx.Query(ctx, `SELECT id::text, title, body, priority, coalesce(link_entity,''), coalesce(link_id,'') FROM life.notices n
		WHERE NOT EXISTS (SELECT 1 FROM life.acked_events a WHERE a.event_id = 'notice:' || n.id::text)
		ORDER BY created_at, id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Event
	for rows.Next() {
		var id, title, body, priority, entity, linkID string
		if err := rows.Scan(&id, &title, &body, &priority, &entity, &linkID); err != nil {
			return nil, err
		}
		n := Notice{Kind: "notice", Title: title, Body: body, Priority: priority}
		if entity != "" {
			n.Link = &Link{Entity: entity, ID: linkID}
		}
		items = append(items, Event{EventKey: noticeEventKey, EventID: "notice:" + id, Detail: "Notice for the person: " + title, Notice: n})
	}
	return items, rows.Err()
}
