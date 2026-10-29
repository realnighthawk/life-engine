package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// An activity is what happened: the engine's evidence stream. It is append-only; a correction changes the text and is
// audited. A completed task or a met commitment writes one deterministically, and an activity may be recorded with no
// task having existed at all ("I called the dentist").
type Activity struct {
	ID          string          `json:"id"`
	Subject     string          `json:"subject" jsonschema:"person (the default) or agent"`
	Actor       string          `json:"actor" jsonschema:"person, agent or external"`
	Kind        string          `json:"kind" jsonschema:"action, occurrence or measurement"`
	Text        string          `json:"text"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Source      string          `json:"source" jsonschema:"person, agent or connector"`
	Origin      string          `json:"origin"`
	DerivedFrom json.RawMessage `json:"derived_from,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type ActivityData struct {
	Actor       string          `json:"actor" jsonschema:"who did it: person (default), agent or external"`
	Kind        string          `json:"kind,omitempty" jsonschema:"action (default), occurrence or measurement"`
	Text        string          `json:"text" jsonschema:"what happened, 1-2000 characters"`
	OccurredAt  string          `json:"occurred_at" jsonschema:"RFC 3339 instant it happened"`
	Source      string          `json:"source,omitempty" jsonschema:"person (default), agent or connector"`
	Subject     string          `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Origin      string          `json:"origin,omitempty" jsonschema:"person (default) or agent: who authored the record"`
	DerivedFrom json.RawMessage `json:"derived_from,omitempty" jsonschema:"when an agent promoted a memory candidate: {bank_id, record_id, snapshot}"`
}
type RecordActivityInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	ActivityData
}
type CorrectActivityInput struct {
	Meta
	ID         string `json:"id" jsonschema:"record UUID"`
	Text       string `json:"text,omitempty" jsonschema:"the corrected text; leave empty to keep it"`
	OccurredAt string `json:"occurred_at,omitempty" jsonschema:"the corrected RFC 3339 instant; leave empty to keep it"`
}
type ListActivitiesInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Actor   string `json:"actor,omitempty" jsonschema:"person, agent or external"`
	Kind    string `json:"kind,omitempty" jsonschema:"action, occurrence or measurement"`
	After   string `json:"after,omitempty" jsonschema:"RFC 3339; only activity at or after this time"`
	Before  string `json:"before,omitempty" jsonschema:"RFC 3339; only activity before this time"`
}

const activityCols = `id::text,subject,actor,kind,text,occurred_at,source,origin,derived_from,created_at`

func scanActivity(row scanner) (Activity, error) {
	var a Activity
	var derived []byte
	if err := row.Scan(&a.ID, &a.Subject, &a.Actor, &a.Kind, &a.Text, &a.OccurredAt, &a.Source, &a.Origin, &derived, &a.CreatedAt); err != nil {
		return a, err
	}
	if len(derived) > 0 {
		a.DerivedFrom = json.RawMessage(derived)
	}
	a.OccurredAt, a.CreatedAt = a.OccurredAt.UTC(), a.CreatedAt.UTC()
	return a, nil
}

func (u *unit) getActivity(ctx context.Context, key string) (Activity, error) {
	k, e := u.resolveRef(ctx, "activities", "", key)
	if e != nil {
		return Activity{}, e
	}
	a, e := scanActivity(u.tx.QueryRow(ctx, "SELECT "+activityCols+" FROM life.activities WHERE id=$1", k))
	if isNoRows(e) {
		e = u.missing(ctx, "activities", "", k)
	}
	return a, e
}

// insertActivity is the one append. Commitments and tasks call it in their own transaction, so the evidence and the
// change land together.
func (u *unit) insertActivity(ctx context.Context, id, subject, actor, kind, text string, occurredAt time.Time, source, origin string, derived json.RawMessage) (Activity, error) {
	key, e := createID(id)
	if e != nil {
		return Activity{}, e
	}
	if actor == "" {
		actor = "person"
	}
	if kind == "" {
		kind = "action"
	}
	if source == "" {
		source = "person"
	}
	if origin == "" {
		origin = "person"
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2000 {
		return Activity{}, invalid("text must be 1-2000 characters")
	}
	if !oneOf(actor, "person", "agent", "external") {
		return Activity{}, invalid("actor must be person, agent or external")
	}
	if !oneOf(kind, "action", "occurrence", "measurement") {
		return Activity{}, invalid("kind must be action, occurrence or measurement")
	}
	if !oneOf(source, "person", "agent", "connector") {
		return Activity{}, invalid("source must be person, agent or connector")
	}
	if subject == "" {
		subject = "person"
	}
	if !oneOf(subject, "person", "agent") {
		return Activity{}, invalid("subject must be person or agent")
	}
	if !oneOf(origin, "person", "agent") {
		return Activity{}, invalid("origin must be person or agent")
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.activities(id,subject,actor,kind,text,occurred_at,source,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, key, subject, actor, kind, text, occurredAt.UTC(), source, origin, derived); e != nil {
		return Activity{}, e
	}
	return u.getActivity(ctx, key)
}

func recordActivity(ctx context.Context, u *unit, in RecordActivityInput) (Activity, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Activity{}, e
	}
	at, e := instant("occurred_at", in.OccurredAt)
	if e != nil {
		return Activity{}, e
	}
	derived, e := derivedFrom(in.DerivedFrom)
	if e != nil {
		return Activity{}, e
	}
	a, e := u.insertActivity(ctx, key, in.Subject, in.Actor, in.Kind, in.Text, at, in.Source, in.Origin, derived)
	if e != nil {
		return a, e
	}
	return a, u.audit(ctx, "activity", a.ID, nil, a)
}

func correctActivity(ctx context.Context, u *unit, in CorrectActivityInput) (Activity, error) {
	old, e := u.getActivity(ctx, in.ID)
	if e != nil {
		return old, e
	}
	text := old.Text
	if in.Text != "" {
		text = strings.TrimSpace(in.Text)
	}
	if text == "" || len(text) > 2000 {
		return old, invalid("text must be 1-2000 characters")
	}
	at := old.OccurredAt
	if in.OccurredAt != "" {
		if at, e = instant("occurred_at", in.OccurredAt); e != nil {
			return old, e
		}
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.activities SET text=$2,occurred_at=$3 WHERE id=$1`, old.ID, text, at); e != nil {
		return old, e
	}
	a, e := u.getActivity(ctx, old.ID)
	if e != nil {
		return a, e
	}
	return a, u.audit(ctx, "activity", a.ID, old, a)
}

func listActivities(ctx context.Context, u *unit, in ListActivitiesInput) (Page[Activity], error) {
	result := Page[Activity]{Items: []Activity{}}
	limit, offset, _, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	subject := in.Subject
	if subject == "" {
		subject = "person"
	}
	if !oneOf(subject, "person", "agent") {
		return result, invalid("subject must be person or agent")
	}
	where, args := []string{"subject=$1"}, []any{subject}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if in.Actor != "" {
		if !oneOf(in.Actor, "person", "agent", "external") {
			return result, invalid("actor must be person, agent or external")
		}
		where = append(where, "actor="+arg(in.Actor))
	}
	if in.Kind != "" {
		if !oneOf(in.Kind, "action", "occurrence", "measurement") {
			return result, invalid("kind must be action, occurrence or measurement")
		}
		where = append(where, "kind="+arg(in.Kind))
	}
	if in.After != "" {
		t, e := instant("after", in.After)
		if e != nil {
			return result, e
		}
		where = append(where, "occurred_at >= "+arg(t))
	}
	if in.Before != "" {
		t, e := instant("before", in.Before)
		if e != nil {
			return result, e
		}
		where = append(where, "occurred_at < "+arg(t))
	}
	if in.Search != "" {
		where = append(where, "text ILIKE "+arg(like(in.Search)))
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.activities WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+activityCols+" FROM life.activities WHERE "+clause+
		" ORDER BY occurred_at DESC, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		a, e := scanActivity(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		result.Items = append(result.Items, a)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}

// A link is one explicit statement between two records. The relation vocabulary is closed; a record never requires a
// link to exist, so the graph is descriptive, not a hierarchy.
type RecordLink struct {
	FromKind  string    `json:"from_kind"`
	FromID    string    `json:"from_id"`
	Relation  string    `json:"relation" jsonschema:"serves, fulfils, affects, constrains or evidences"`
	ToKind    string    `json:"to_kind"`
	ToID      string    `json:"to_id"`
	CreatedAt time.Time `json:"created_at"`
}

type SetLinkInput struct {
	Meta
	FromKind string `json:"from_kind" jsonschema:"intention, commitment, decision, state, activity, task, event, goal or item"`
	FromID   string `json:"from_id"`
	Relation string `json:"relation" jsonschema:"serves, fulfils, affects, constrains or evidences"`
	ToKind   string `json:"to_kind" jsonschema:"intention, commitment, decision, state, activity, task, event, goal or item"`
	ToID     string `json:"to_id"`
}
type RemoveLinkInput struct {
	Meta
	FromKind string `json:"from_kind"`
	FromID   string `json:"from_id"`
	Relation string `json:"relation"`
	ToKind   string `json:"to_kind"`
	ToID     string `json:"to_id"`
}
type ListLinksInput struct {
	Kind      string `json:"kind" jsonschema:"the record's kind"`
	ID        string `json:"id" jsonschema:"the record's UUID"`
	Relation  string `json:"relation,omitempty" jsonschema:"only this relation"`
	Direction string `json:"direction,omitempty" jsonschema:"from, to or both (default)"`
}

var linkKinds = []string{"intention", "commitment", "decision", "state", "activity", "task", "event", "goal", "item"}
var linkRelations = []string{"serves", "fulfils", "affects", "constrains", "evidences"}

func setLink(ctx context.Context, u *unit, in SetLinkInput) (RecordLink, error) {
	if !oneOf(in.FromKind, linkKinds...) || !oneOf(in.ToKind, linkKinds...) {
		return RecordLink{}, invalid("from_kind and to_kind must name a record kind")
	}
	if !oneOf(in.Relation, linkRelations...) {
		return RecordLink{}, invalid("relation must be serves, fulfils, affects, constrains or evidences")
	}
	from, e := id(in.FromID)
	if e != nil {
		return RecordLink{}, invalid("from_id must be a UUID")
	}
	to, e := id(in.ToID)
	if e != nil {
		return RecordLink{}, invalid("to_id must be a UUID")
	}
	if from == to && in.FromKind == in.ToKind {
		return RecordLink{}, invalid("a record cannot link to itself")
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.links(from_kind,from_id,relation,to_kind,to_id) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT DO NOTHING`, in.FromKind, from, in.Relation, in.ToKind, to); e != nil {
		return RecordLink{}, e
	}
	return RecordLink{FromKind: in.FromKind, FromID: from, Relation: in.Relation, ToKind: in.ToKind, ToID: to}, nil
}

func removeLink(ctx context.Context, u *unit, in RemoveLinkInput) (struct{}, error) {
	from, e := id(in.FromID)
	if e != nil {
		return struct{}{}, invalid("from_id must be a UUID")
	}
	to, e := id(in.ToID)
	if e != nil {
		return struct{}{}, invalid("to_id must be a UUID")
	}
	_, e = u.tx.Exec(ctx, `DELETE FROM life.links WHERE from_kind=$1 AND from_id=$2 AND relation=$3 AND to_kind=$4 AND to_id=$5`,
		in.FromKind, from, in.Relation, in.ToKind, to)
	return struct{}{}, e
}

func listLinks(ctx context.Context, u *unit, in ListLinksInput) (Page[RecordLink], error) {
	result := Page[RecordLink]{Items: []RecordLink{}}
	if !oneOf(in.Kind, linkKinds...) {
		return result, invalid("kind must name a record kind")
	}
	record, e := id(in.ID)
	if e != nil {
		return result, invalid("id must be a UUID")
	}
	direction := in.Direction
	if direction == "" {
		direction = "both"
	}
	if !oneOf(direction, "from", "to", "both") {
		return result, invalid("direction must be from, to or both")
	}
	where := []string{"((from_kind=$1 AND from_id=$2) OR (to_kind=$1 AND to_id=$2))"}
	args := []any{in.Kind, record}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if direction == "from" {
		where = []string{"from_kind=$1 AND from_id=$2"}
	}
	if direction == "to" {
		where = []string{"to_kind=$1 AND to_id=$2"}
	}
	if in.Relation != "" {
		if !oneOf(in.Relation, linkRelations...) {
			return result, invalid("relation must be serves, fulfils, affects, constrains or evidences")
		}
		where = append(where, "relation="+arg(in.Relation))
	}
	rows, e := u.tx.Query(ctx, `SELECT from_kind,from_id::text,relation,to_kind,to_id::text,created_at FROM life.links WHERE `+
		strings.Join(where, " AND ")+" ORDER BY created_at, from_kind, from_id, relation, to_kind, to_id", args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		var l RecordLink
		if e = rows.Scan(&l.FromKind, &l.FromID, &l.Relation, &l.ToKind, &l.ToID, &l.CreatedAt); e != nil {
			return result, e
		}
		l.CreatedAt = l.CreatedAt.UTC()
		result.Items = append(result.Items, l)
	}
	result.Total = len(result.Items)
	return result, rows.Err()
}
