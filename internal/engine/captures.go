package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Capture struct {
	ID         string     `json:"id"`
	Text       string     `json:"text"`
	CapturedAt time.Time  `json:"captured_at"`
	Source     string     `json:"source" jsonschema:"user, voice or agent"`
	SessionID  string     `json:"session_id,omitempty"`
	TurnSeq    *int64     `json:"turn_seq"`
	State      string     `json:"state" jsonschema:"open or resolved"`
	ResolvedAs string     `json:"resolved_as,omitempty" jsonschema:"what it became: reminder, plan_item, journal_entry, note, goal or dismissed"`
	ResolvedID string     `json:"resolved_id,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Version    int64      `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type CreateCaptureInput struct {
	Meta
	ID         string  `json:"id,omitempty" jsonschema:"client-generated UUID, so an offline retry cannot duplicate"`
	Text       string  `json:"text" jsonschema:"what the person said or typed, unprocessed, 1-2000 characters"`
	CapturedAt *string `json:"captured_at,omitempty" jsonschema:"RFC 3339 when it was captured on the device; default now"`
	Source     string  `json:"source,omitempty" jsonschema:"user (default), voice or agent"`
	SessionID  string  `json:"session_id,omitempty"`
	TurnSeq    *int64  `json:"turn_seq,omitempty"`
}
type ListCapturesInput struct {
	ListInput
	Status string `json:"status,omitempty" jsonschema:"open (default), resolved or all"`
}
type ResolveCaptureInput struct {
	EditInput
	As       string `json:"as" jsonschema:"what it became: reminder, plan_item, journal_entry, note, goal, or dismissed"`
	EntityID string `json:"entity_id,omitempty" jsonschema:"the record you created from it; required unless dismissed"`
}

const captureCols = `id::text,text,captured_at,source,coalesce(session_id,''),turn_seq,state,coalesce(resolved_as,''),coalesce(resolved_id::text,''),resolved_at,version,created_at,updated_at`

func scanCapture(row scanner) (Capture, error) {
	var c Capture
	err := row.Scan(&c.ID, &c.Text, &c.CapturedAt, &c.Source, &c.SessionID, &c.TurnSeq, &c.State, &c.ResolvedAs, &c.ResolvedID, &c.ResolvedAt, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	c.CapturedAt, c.CreatedAt, c.UpdatedAt = c.CapturedAt.UTC(), c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	utcPtr(c.ResolvedAt)
	return c, err
}

func (u *unit) getCapture(ctx context.Context, key string, forUpdate bool) (Capture, error) {
	k, e := id(key)
	if e != nil {
		return Capture{}, e
	}
	q := "SELECT " + captureCols + " FROM life.captures WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanCapture(u.tx.QueryRow(ctx, q, k))
}

func createCapture(ctx context.Context, u *unit, in CreateCaptureInput) (Capture, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Capture{}, e
	}
	text := strings.TrimSpace(in.Text)
	if text == "" || len(text) > 2000 {
		return Capture{}, invalid("text must be 1-2000 characters")
	}
	source := in.Source
	if source == "" {
		source = "user"
	}
	if !oneOf(source, "user", "voice", "agent") {
		return Capture{}, invalid("source must be user, voice or agent")
	}
	at, e := u.now(ctx)
	if e != nil {
		return Capture{}, e
	}
	if in.CapturedAt != nil {
		if at, e = instant("captured_at", *in.CapturedAt); e != nil {
			return Capture{}, e
		}
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.captures(id,text,captured_at,source,session_id,turn_seq) VALUES($1,$2,$3,$4,NULLIF($5,''),$6)`, key, text, at, source, in.SessionID, in.TurnSeq); e != nil {
		return Capture{}, e
	}
	c, e := u.getCapture(ctx, key, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "capture", key, nil, c)
}

func listCaptures(ctx context.Context, u *unit, in ListCapturesInput) (Page[Capture], error) {
	result := Page[Capture]{Items: []Capture{}}
	limit, offset, _, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	status := in.Status
	if status == "" {
		status = "open"
	}
	if !oneOf(status, "open", "resolved", "all") {
		return result, invalid("status must be open, resolved or all")
	}
	where, args := []string{"true"}, []any{}
	if status != "all" {
		args = append(args, status)
		where = append(where, "state=$1")
	}
	if in.Search != "" {
		args = append(args, like(in.Search))
		where = append(where, "text ILIKE $"+itoa(len(args)))
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.captures WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	args = append(args, limit, offset)
	rows, e := u.tx.Query(ctx, "SELECT "+captureCols+" FROM life.captures WHERE "+clause+" ORDER BY captured_at DESC, id LIMIT $"+itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if e != nil {
		return result, e
	}
	items, e := collect(rows, scanCapture)
	if e != nil {
		return result, e
	}
	if items != nil {
		result.Items = items
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}

var captureTargets = map[string]string{"reminder": "reminders", "plan_item": "plan_items", "journal_entry": "journal_entries", "note": "notes", "goal": "goals"}

func resolveCapture(ctx context.Context, u *unit, in ResolveCaptureInput) (Capture, error) {
	old, e := u.getCapture(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.State != "open" {
		return old, conflict("Capture is already resolved")
	}
	target := ""
	if in.As != "dismissed" {
		table, ok := captureTargets[in.As]
		if !ok {
			return old, invalid("as must be reminder, plan_item, journal_entry, note, goal or dismissed")
		}
		if target, e = u.refExists(ctx, table, in.EntityID); e != nil || target == "" {
			if e == nil {
				e = invalid("entity_id is required unless the capture is dismissed")
			}
			return old, e
		}
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.captures SET state='resolved',resolved_as=$2,resolved_id=NULLIF($3,'')::uuid,resolved_at=now(),version=version+1,updated_at=now() WHERE id=$1`, old.ID, in.As, target); e != nil {
		return old, e
	}
	c, e := u.getCapture(ctx, old.ID, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "capture", c.ID, old, c)
}

const captureEventKey = "life:capture"

// pendingCaptures asks the agent (by default) to turn each open capture into the right record. A capture is raised once;
// it stays open for the person to resolve by hand if the agent does not.
func (u *unit) pendingCaptures(ctx context.Context) ([]Event, error) {
	rows, err := u.tx.Query(ctx, `SELECT `+captureCols+` FROM life.captures c
		WHERE c.state='open' AND NOT EXISTS (SELECT 1 FROM life.acked_events a WHERE a.event_id = 'capture:' || c.id::text || ':' || floor(extract(epoch FROM c.captured_at))::bigint::text)
		ORDER BY c.captured_at, c.id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	caps, err := collect(rows, scanCapture)
	if err != nil {
		return nil, err
	}
	var items []Event
	for _, c := range caps {
		items = append(items, Event{
			EventKey: captureEventKey,
			EventID:  "capture:" + c.ID + ":" + strconv.FormatInt(c.CapturedAt.Unix(), 10),
			Detail:   fmt.Sprintf("The person captured: %q. Decide what it is (reminder, plan item, journal entry, note or goal), create it, then resolve the capture.", c.Text),
			Notice:   Notice{Kind: "capture.open", Title: "Captured", Body: c.Text, Priority: "normal", Link: &Link{Entity: "capture", ID: c.ID}},
		})
	}
	return items, nil
}
