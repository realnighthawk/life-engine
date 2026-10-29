package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// A watch is the agent's standing instruction to be woken when an event happens: "when the budget crosses its line, check
// the spend and tell the person". The engine owns the record (project-hub ADR 0009) and mcp-hub only carries events to it.
// An event arrives at events_deliver; every live watch for its key gets a firing row (the dedupe and the outbox), and a
// wake is sent to the watch's conversation from that row, retried until it is.

type Watch struct {
	ID          string     `json:"id"`
	Name        string     `json:"name" jsonschema:"the watch's identity within the conversation"`
	SessionKey  string     `json:"session_key" jsonschema:"the conversation a firing wakes"`
	EventKey    string     `json:"event_key" jsonschema:"the event watched, exactly as the engine that raises it names it"`
	Objective   string     `json:"objective" jsonschema:"what to do when woken"`
	IntentionID *string    `json:"intention_id" jsonschema:"the intention this watch serves, or null"`
	ExpiresAt   *time.Time `json:"expires_at" jsonschema:"the watch stops firing at this time, or null"`
	Version     int64      `json:"version"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

const watchCols = `id::text,name,session_key,event_key,objective,intention_id::text,expires_at,version,deleted_at,created_at,updated_at`

func scanWatch(row scanner) (Watch, error) {
	var w Watch
	err := row.Scan(&w.ID, &w.Name, &w.SessionKey, &w.EventKey, &w.Objective, &w.IntentionID, &w.ExpiresAt, &w.Version, &w.DeletedAt, &w.CreatedAt, &w.UpdatedAt)
	utcPtr(w.ExpiresAt)
	utcPtr(w.DeletedAt)
	return w, err
}

type CreateWatchInput struct {
	Meta
	Name        string `json:"name" jsonschema:"short, lowercase letters, digits and dashes, 1-60 characters; arming the same name again revises that watch"`
	EventKey    string `json:"event_key" jsonschema:"the event to watch, exactly as the engine that raises it names it"`
	Objective   string `json:"objective" jsonschema:"what you should do when you are woken, up to 2000 characters: the event says only what happened"`
	IntentionID string `json:"intention_id,omitempty" jsonschema:"UUID of the intention this watch serves; optional"`
	ExpiresAt   string `json:"expires_at,omitempty" jsonschema:"RFC 3339 instant after which the watch stops firing; optional"`
}
type UpdateWatchInput struct {
	EditInput
	EventKey  string `json:"event_key,omitempty" jsonschema:"the event to watch instead; left out, it stays"`
	Objective string `json:"objective,omitempty" jsonschema:"what to do when woken, instead; left out, it stays"`
	ExpiresAt string `json:"expires_at,omitempty" jsonschema:"RFC 3339 instant after which the watch stops firing; left out, it stays"`
}
type ListWatchesInput struct {
	ListInput
}

var watchName = func(name string) error {
	if len(name) < 1 || len(name) > 60 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || name[0] == '-' {
		return invalid("name must be 1-60 lowercase letters, digits and dashes, not starting with a dash")
	}
	return nil
}

func (u *unit) getWatch(ctx context.Context, key string, forUpdate bool) (Watch, error) {
	k, e := id(key)
	if e != nil {
		return Watch{}, e
	}
	q := "SELECT " + watchCols + " FROM life.watches WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	w, e := scanWatch(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = notFound()
	}
	return w, e
}

// scope is the conversation a watch belongs to: the caller's, normalised to the user-stable session. A call from outside a
// conversation (the HTTP API) has none and sees every watch.
func (u *unit) scope() string {
	if u.session == "" {
		return ""
	}
	return sessionScope(u.session)
}

func expiry(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, e := instant("expires_at", value)
	if e != nil {
		return nil, e
	}
	return &t, nil
}

// createWatch arms a watch, or revises the one already armed under this name: the name is the identity, so two arms of the
// same request collide on purpose.
func createWatch(ctx context.Context, u *unit, in CreateWatchInput) (Watch, error) {
	if u.scope() == "" {
		return Watch{}, invalid("a watch wakes the conversation it was armed from; arm it from a conversation")
	}
	if e := watchName(in.Name); e != nil {
		return Watch{}, e
	}
	event, objective := strings.TrimSpace(in.EventKey), strings.TrimSpace(in.Objective)
	if event == "" || len(event) > 200 {
		return Watch{}, invalid("event_key must be 1-200 characters")
	}
	if objective == "" || len(objective) > 2000 {
		return Watch{}, invalid("objective must be 1-2000 characters: it is what you will be asked to do when woken")
	}
	exp, e := expiry(in.ExpiresAt)
	if e != nil {
		return Watch{}, e
	}
	if in.IntentionID != "" {
		if _, e = id(in.IntentionID); e != nil {
			return Watch{}, invalid("intention_id must be a UUID")
		}
		var found bool
		if e = u.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM life.intentions WHERE id=$1 AND deleted_at IS NULL)", in.IntentionID).Scan(&found); e != nil {
			return Watch{}, e
		}
		if !found {
			return Watch{}, invalid("intention_id points at an intention that does not exist or is archived")
		}
	}
	var existing string
	err := u.tx.QueryRow(ctx, "SELECT id::text FROM life.watches WHERE session_key=$1 AND name=$2 AND deleted_at IS NULL FOR UPDATE", u.scope(), in.Name).Scan(&existing)
	if err != nil && !isNoRows(err) {
		return Watch{}, err
	}
	if isNoRows(err) {
		var newID string
		if err = u.tx.QueryRow(ctx, `INSERT INTO life.watches(name,session_key,event_key,objective,intention_id,expires_at)
			VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6) RETURNING id::text`, in.Name, u.scope(), event, objective, in.IntentionID, exp).Scan(&newID); err != nil {
			return Watch{}, err
		}
		w, e := u.getWatch(ctx, newID, false)
		if e != nil {
			return w, e
		}
		return w, u.audit(ctx, "watch", w.ID, nil, w)
	}
	old, e := u.getWatch(ctx, existing, true)
	if e != nil {
		return old, e
	}
	if _, err = u.tx.Exec(ctx, `UPDATE life.watches SET event_key=$2,objective=$3,intention_id=NULLIF($4,'')::uuid,expires_at=$5,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, event, objective, in.IntentionID, exp); err != nil {
		return old, err
	}
	return u.watchChanged(ctx, old)
}

func (u *unit) watchChanged(ctx context.Context, old Watch) (Watch, error) {
	w, e := u.getWatch(ctx, old.ID, false)
	if e != nil {
		return w, e
	}
	return w, u.audit(ctx, "watch", w.ID, old, w)
}

// updateWatch changes what a watch listens for or does; a field left out stays, so rewording an objective cannot silently
// stop the watch watching the event it was armed for.
func updateWatch(ctx context.Context, u *unit, in UpdateWatchInput) (Watch, error) {
	old, e := u.getWatch(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("The watch was cancelled; arm it again")
	}
	event, objective, exp := old.EventKey, old.Objective, old.ExpiresAt
	if v := strings.TrimSpace(in.EventKey); v != "" {
		if len(v) > 200 {
			return old, invalid("event_key must be 1-200 characters")
		}
		event = v
	}
	if v := strings.TrimSpace(in.Objective); v != "" {
		if len(v) > 2000 {
			return old, invalid("objective must be 1-2000 characters")
		}
		objective = v
	}
	if in.ExpiresAt != "" {
		if exp, e = expiry(in.ExpiresAt); e != nil {
			return old, e
		}
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.watches SET event_key=$2,objective=$3,expires_at=$4,version=version+1,updated_at=now() WHERE id=$1`, old.ID, event, objective, exp); e != nil {
		return old, e
	}
	return u.watchChanged(ctx, old)
}

// deleteWatch cancels a watch. It stops firing at once; a firing already recorded is still sent.
func deleteWatch(ctx context.Context, u *unit, in EditInput) (Watch, error) {
	old, e := u.getWatch(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("It is already cancelled")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.watches SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.watchChanged(ctx, old)
}

func listWatches(ctx context.Context, u *unit, in ListWatchesInput) (Page[Watch], error) {
	result := Page[Watch]{Items: []Watch{}}
	limit, offset, state, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	where, args := []string{"true"}, []any{}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if scope := u.scope(); scope != "" {
		where = append(where, "session_key="+arg(scope))
	}
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if in.Search != "" {
		p := arg(like(in.Search))
		where = append(where, "(name ILIKE "+p+" OR objective ILIKE "+p+" OR event_key ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.watches WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+watchCols+" FROM life.watches WHERE "+clause+" ORDER BY name, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		w, e := scanWatch(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, w)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}

// DeliverEventInput is one notice an engine recorded, as the hub forwards it.
type DeliverEventInput struct {
	EventKey string `json:"event_key" jsonschema:"the class of notice, as the engine that raised it names it"`
	EventID  string `json:"event_id" jsonschema:"this occurrence, durable and deterministic so a re-delivery is recognised"`
	Detail   string `json:"detail" jsonschema:"what happened, in one sentence"`
	Source   string `json:"source,omitempty" jsonschema:"the connection that raised it"`
}
type DeliverEventResult struct {
	Matched int `json:"matched" jsonschema:"live watches for this event_key"`
	Queued  int `json:"queued" jsonschema:"wakes newly queued; fewer than matched when this event was delivered before"`
}

// deliverEvent records a firing for every live watch of the event's key. Idempotent by construction: a firing is keyed by
// (watch, event_id), so the hub re-delivering an event queues nothing twice. The wake itself is sent from the firing row
// (SendWatchWakes), not here, so this answers as soon as the event is durably accepted.
func (u *unit) deliverEvent(ctx context.Context, in DeliverEventInput) (DeliverEventResult, error) {
	var out DeliverEventResult
	if in.EventKey == "" || len(in.EventKey) > 200 || in.EventID == "" || len(in.EventID) > 200 {
		return out, invalid("event_key and event_id must be 1-200 characters")
	}
	if len(in.Detail) > 4000 {
		in.Detail = in.Detail[:4000]
	}
	rows, err := u.tx.Query(ctx, `SELECT id::text FROM life.watches WHERE event_key=$1 AND deleted_at IS NULL
		AND (expires_at IS NULL OR expires_at > now()) ORDER BY id`, in.EventKey)
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var wid string
		if err = rows.Scan(&wid); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, wid)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Matched = len(ids)
	for _, wid := range ids {
		tag, err := u.tx.Exec(ctx, `INSERT INTO life.watch_firings(watch_id,event_id,detail,source) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			wid, in.EventID, in.Detail, in.Source)
		if err != nil {
			return out, err
		}
		out.Queued += int(tag.RowsAffected())
	}
	return out, nil
}

// SendWatchWakes sends the wake for every recorded firing that has not been sent, and marks each one. The wake workflow is
// named for the firing, so starting it twice (a crash between starting and marking) is one wake. A failure is returned and
// the firing stays unsent for the next pass.
func (s *Service) SendWatchWakes(ctx context.Context) error {
	if s.waker == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT f.watch_id::text, f.event_id, f.detail, w.name, w.session_key, w.objective
		FROM life.watch_firings f JOIN life.watches w ON w.id=f.watch_id WHERE f.started_at IS NULL ORDER BY f.created_at LIMIT 100`)
	if err != nil {
		return err
	}
	type pending struct {
		watch, event, detail, name, session, objective string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.watch, &p.event, &p.detail, &p.name, &p.session, &p.objective); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var firstErr error
	for _, p := range todo {
		wake := FireWake{SessionKey: p.session, WakeID: fmt.Sprintf("watch:%s:%s", p.name, p.event), Objective: p.objective, Why: p.detail}
		if err = s.waker.Wake(ctx, "watch:"+p.watch+":"+p.event, wake); err != nil {
			slog.Error("watch wake failed", "watch", p.name, "event", p.event, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if _, err = s.pool.Exec(ctx, "UPDATE life.watch_firings SET started_at=now() WHERE watch_id=$1 AND event_id=$2", p.watch, p.event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// taskDone is the event a one-off task being done raises: "task_done:<task id>". A watch armed on that key wakes its
// conversation with the task's own result, which is how the agent follows its own work ("when the plan's task is done, check
// it") without polling. The event id carries the task's version, so reopening and completing it again fires again. It runs in
// the completing transaction, so a watch cannot miss the completion or see it twice.
func (u *unit) taskDone(ctx context.Context, it Task) error {
	detail := fmt.Sprintf("Task %q is done.", it.Title)
	if it.Result != "" {
		detail += " " + it.Result
	}
	_, err := u.deliverEvent(ctx, DeliverEventInput{
		EventKey: "task_done:" + it.ID, EventID: fmt.Sprintf("task_done:%s@%d", it.ID, it.Version), Detail: detail, Source: "life",
	})
	return err
}
