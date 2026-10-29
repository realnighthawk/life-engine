package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// A state is a confirmed fact about the person, their environment or their resources: a fact, a preference, a constraint
// or a need. The engine holds only confirmed states; a hypothesis lives in memory until the person promotes it. `value`
// is open JSON so "vegetarian", 3 and a location all fit one shape.
type State struct {
	ID          string          `json:"id"`
	Subject     string          `json:"subject" jsonschema:"person (the default) or agent"`
	Kind        string          `json:"kind" jsonschema:"fact, preference, constraint or need"`
	Topic       string          `json:"topic" jsonschema:"what it is about, such as running or passport"`
	Predicate   string          `json:"predicate" jsonschema:"what is true of it, such as current distance"`
	Value       json.RawMessage `json:"value,omitempty" jsonschema:"the value, as any JSON: a string, number, boolean or object"`
	Notes       string          `json:"notes"`
	ValidFrom   *time.Time      `json:"valid_from" jsonschema:"when it became true, or null"`
	ValidTo     *time.Time      `json:"valid_to" jsonschema:"when it stopped being true, or null"`
	Source      string          `json:"source" jsonschema:"person, agent or observation"`
	Status      string          `json:"status" jsonschema:"confirmed, retracted or superseded"`
	Origin      string          `json:"origin"`
	DerivedFrom json.RawMessage `json:"derived_from,omitempty"`
	Version     int64           `json:"version"`
	DeletedAt   *time.Time      `json:"deleted_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type StateData struct {
	Kind        string          `json:"kind,omitempty" jsonschema:"fact (default), preference, constraint or need"`
	Topic       string          `json:"topic" jsonschema:"what it is about, 1-200 characters"`
	Predicate   string          `json:"predicate" jsonschema:"what is true of it, 1-200 characters"`
	Value       json.RawMessage `json:"value,omitempty" jsonschema:"any JSON value"`
	Notes       string          `json:"notes,omitempty" jsonschema:"up to 2000 characters"`
	ValidFrom   *string         `json:"valid_from,omitempty" jsonschema:"RFC 3339 instant it became true"`
	ValidTo     *string         `json:"valid_to,omitempty" jsonschema:"RFC 3339 instant it stopped being true"`
	Source      string          `json:"source,omitempty" jsonschema:"person (default), agent or observation"`
	Status      string          `json:"status,omitempty" jsonschema:"confirmed (default), retracted or superseded"`
	Subject     string          `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Origin      string          `json:"origin,omitempty" jsonschema:"person (default) or agent: who authored it"`
	DerivedFrom json.RawMessage `json:"derived_from,omitempty" jsonschema:"when an agent promoted a memory candidate: {bank_id, record_id, snapshot}"`
}
type CreateStateInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	StateData
}
type UpdateStateInput struct {
	EditInput
	StateData
}
type ListStatesInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Kind    string `json:"kind,omitempty" jsonschema:"fact, preference, constraint, need or all"`
	Status  string `json:"status,omitempty" jsonschema:"confirmed (default), retracted, superseded or all"`
	Topic   string `json:"topic,omitempty" jsonschema:"only states about this topic"`
}

const stateCols = `id::text,subject,kind,topic,predicate,value,notes,valid_from,valid_to,source,status,origin,derived_from,version,deleted_at,created_at,updated_at`

func scanState(row scanner) (State, error) {
	var s State
	var value, derived []byte
	err := row.Scan(&s.ID, &s.Subject, &s.Kind, &s.Topic, &s.Predicate, &value, &s.Notes, &s.ValidFrom, &s.ValidTo,
		&s.Source, &s.Status, &s.Origin, &derived, &s.Version, &s.DeletedAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	if len(value) > 0 {
		s.Value = json.RawMessage(value)
	}
	if len(derived) > 0 {
		s.DerivedFrom = json.RawMessage(derived)
	}
	utcPtr(s.ValidFrom)
	utcPtr(s.ValidTo)
	utcPtr(s.DeletedAt)
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return s, nil
}

func (u *unit) getState(ctx context.Context, key string, forUpdate bool) (State, error) {
	k, e := u.resolveRef(ctx, "states", "", key)
	if e != nil {
		return State{}, e
	}
	q := "SELECT " + stateCols + " FROM life.states WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	s, e := scanState(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "states", "", k)
	}
	return s, e
}

type stateValues struct {
	kind, topic, predicate, notes, source, status, subject, origin string
	value                                                          json.RawMessage
	from, to                                                       *time.Time
	derived                                                        json.RawMessage
}

func (d StateData) values() (stateValues, error) {
	v := stateValues{kind: d.Kind, topic: strings.TrimSpace(d.Topic), predicate: strings.TrimSpace(d.Predicate), notes: d.Notes,
		source: d.Source, status: d.Status, subject: d.Subject, origin: d.Origin, value: d.Value}
	if v.topic == "" || len(v.topic) > 200 || v.predicate == "" || len(v.predicate) > 200 {
		return v, invalid("topic and predicate must be 1-200 characters")
	}
	if len(v.notes) > 2000 {
		return v, invalid("notes must be at most 2000 characters")
	}
	if v.kind == "" {
		v.kind = "fact"
	}
	if !oneOf(v.kind, "fact", "preference", "constraint", "need") {
		return v, invalid("kind must be fact, preference, constraint or need")
	}
	if v.source == "" {
		v.source = "person"
	}
	if !oneOf(v.source, "person", "agent", "observation") {
		return v, invalid("source must be person, agent or observation")
	}
	if v.status == "" {
		v.status = "confirmed"
	}
	if !oneOf(v.status, "confirmed", "retracted", "superseded") {
		return v, invalid("status must be confirmed, retracted or superseded")
	}
	if v.subject == "" {
		v.subject = "person"
	}
	if !oneOf(v.subject, "person", "agent") {
		return v, invalid("subject must be person or agent")
	}
	if v.origin == "" {
		v.origin = "person"
	}
	if !oneOf(v.origin, "person", "agent") {
		return v, invalid("origin must be person or agent")
	}
	if d.ValidFrom != nil {
		t, e := instant("valid_from", *d.ValidFrom)
		if e != nil {
			return v, e
		}
		v.from = &t
	}
	if d.ValidTo != nil {
		t, e := instant("valid_to", *d.ValidTo)
		if e != nil {
			return v, e
		}
		v.to = &t
	}
	if v.from != nil && v.to != nil && v.to.Before(*v.from) {
		return v, invalid("valid_to must not be before valid_from")
	}
	derived, e := derivedFrom(d.DerivedFrom)
	if e != nil {
		return v, e
	}
	v.derived = derived
	return v, nil
}

func createState(ctx context.Context, u *unit, in CreateStateInput) (State, error) {
	key, e := createID(in.ID)
	if e != nil {
		return State{}, e
	}
	v, e := in.StateData.values()
	if e != nil {
		return State{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.states(id,subject,kind,topic,predicate,value,notes,valid_from,valid_to,source,status,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::timestamptz,$9::timestamptz,$10,$11,$12,$13)`,
		key, v.subject, v.kind, v.topic, v.predicate, v.value, v.notes, v.from, v.to, v.source, v.status, v.origin, v.derived); e != nil {
		return State{}, e
	}
	s, e := u.getState(ctx, key, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "state", key, nil, s)
}

func updateState(ctx context.Context, u *unit, in UpdateStateInput) (State, error) {
	old, e := u.getState(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the state before editing it")
	}
	v, e := in.StateData.values()
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.states SET subject=$2,kind=$3,topic=$4,predicate=$5,value=$6,notes=$7,valid_from=$8::timestamptz,
		valid_to=$9::timestamptz,source=$10,status=$11,origin=$12,derived_from=$13,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.subject, v.kind, v.topic, v.predicate, v.value, v.notes, v.from, v.to, v.source, v.status, v.origin, v.derived); e != nil {
		return old, e
	}
	s, e := u.getState(ctx, old.ID, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "state", s.ID, old, s)
}

func retractState(ctx context.Context, u *unit, in EditInput) (State, error) {
	old, e := u.getState(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.Status == "retracted" {
		return old, conflict("State is already retracted")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.states SET status='retracted',version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	s, e := u.getState(ctx, old.ID, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "state", s.ID, old, s)
}

func lifecycleState(ctx context.Context, u *unit, in EditInput, restore bool) (State, error) {
	old, e := u.getState(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("State is not deleted")
		}
		return old, conflict("State is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.states SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	s, e := u.getState(ctx, old.ID, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "state", s.ID, old, s)
}

func listStates(ctx context.Context, u *unit, in ListStatesInput) (Page[State], error) {
	result := Page[State]{Items: []State{}}
	limit, offset, state, e := paging(in.ListInput)
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
	if kind := in.Kind; kind != "" && kind != "all" {
		if !oneOf(kind, "fact", "preference", "constraint", "need") {
			return result, invalid("kind must be fact, preference, constraint, need or all")
		}
		where = append(where, "kind="+arg(kind))
	}
	status := in.Status
	if status == "" {
		status = "confirmed"
	}
	if status != "all" {
		if !oneOf(status, "confirmed", "retracted", "superseded") {
			return result, invalid("status must be confirmed, retracted, superseded or all")
		}
		where = append(where, "status="+arg(status))
	}
	if in.Topic != "" {
		where = append(where, "topic="+arg(in.Topic))
	}
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if in.Search != "" {
		p := arg(like(in.Search))
		where = append(where, "(topic ILIKE "+p+" OR predicate ILIKE "+p+" OR notes ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.states WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+stateCols+" FROM life.states WHERE "+clause+
		" ORDER BY topic, predicate, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		s, e := scanState(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		result.Items = append(result.Items, s)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}
