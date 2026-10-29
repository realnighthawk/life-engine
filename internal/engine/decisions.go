package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A decision is a choice the person is facing or has made. It is also the promotion gate: `kind=change` carries a batch
// of ordinary create operations as its effect, applied all-or-nothing when the option named `accept` is chosen. A change
// decision that came from memory requires a person-sourced confirmation, so the agent cannot promote its own proposal.
type DecisionOption struct {
	ID    string `json:"id" jsonschema:"stable id used to resolve, such as accept, gym or outdoors, 1-40 characters"`
	Label string `json:"label" jsonschema:"what the person reads, 1-200 characters"`
}

type Decision struct {
	ID           string           `json:"id"`
	Subject      string           `json:"subject"`
	Question     string           `json:"question"`
	Kind         string           `json:"kind" jsonschema:"choice or change"`
	Options      []DecisionOption `json:"options"`
	Status       string           `json:"status" jsonschema:"open, decided, deferred or expired"`
	ChosenOption *string          `json:"chosen_option"`
	Rationale    string           `json:"rationale"`
	DecidedBy    *string          `json:"decided_by" jsonschema:"person or agent"`
	DecidedAt    *time.Time       `json:"decided_at"`
	ExpiresAt    *time.Time       `json:"expires_at"`
	Effect       json.RawMessage  `json:"effect,omitempty" jsonschema:"for a change: the batch applied when accept is chosen"`
	Origin       string           `json:"origin"`
	DerivedFrom  json.RawMessage  `json:"derived_from,omitempty"`
	Version      int64            `json:"version"`
	DeletedAt    *time.Time       `json:"deleted_at"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

type DecisionData struct {
	Question    string           `json:"question" jsonschema:"the choice, 1-500 characters, in the person's words"`
	Kind        string           `json:"kind,omitempty" jsonschema:"choice (default) or change"`
	Options     []DecisionOption `json:"options" jsonschema:"2-10 options; a change needs an option with id accept"`
	ExpiresAt   *string          `json:"expires_at,omitempty" jsonschema:"RFC 3339 instant it stops being open; optional"`
	Effect      json.RawMessage  `json:"effect,omitempty" jsonschema:"for change: a batch plan (the same shape as batch_apply) applied when accept is chosen"`
	Subject     string           `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Origin      string           `json:"origin,omitempty" jsonschema:"person (default) or agent: who authored it"`
	DerivedFrom json.RawMessage  `json:"derived_from,omitempty" jsonschema:"when an agent promoted a memory candidate: {bank_id, record_id, snapshot}"`
}
type CreateDecisionInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	DecisionData
}
type UpdateDecisionInput struct {
	EditInput
	DecisionData
}
type ResolveDecisionInput struct {
	EditInput
	Option       string        `json:"option" jsonschema:"the id of the option chosen"`
	Rationale    string        `json:"rationale,omitempty" jsonschema:"why, in the person's words, up to 2000 characters"`
	DecidedBy    string        `json:"decided_by,omitempty" jsonschema:"person (default) or agent"`
	Confirmation *Confirmation `json:"confirmation,omitempty" jsonschema:"required for a change decision derived from memory: a person-sourced confirmation"`
}
type Confirmation struct {
	Source string `json:"source" jsonschema:"client or turn"`
	Ref    string `json:"ref" jsonschema:"the client action id, or the agent turn the person answered in"`
}
type DeferDecisionInput struct {
	EditInput
	Until string `json:"until" jsonschema:"RFC 3339 instant it becomes open again; optional"`
}
type ListDecisionsInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Status  string `json:"status,omitempty" jsonschema:"open (default), decided, deferred, expired or all"`
}

const decisionCols = `id::text,subject,question,kind,options,status,chosen_option,rationale,decided_by,decided_at,expires_at,effect,origin,derived_from,version,deleted_at,created_at,updated_at`

func scanDecision(row scanner) (Decision, error) {
	var d Decision
	var options, effect, derived []byte
	err := row.Scan(&d.ID, &d.Subject, &d.Question, &d.Kind, &options, &d.Status, &d.ChosenOption, &d.Rationale, &d.DecidedBy,
		&d.DecidedAt, &d.ExpiresAt, &effect, &d.Origin, &derived, &d.Version, &d.DeletedAt, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(options, &d.Options); err != nil {
		return d, err
	}
	if len(effect) > 0 {
		d.Effect = json.RawMessage(effect)
	}
	if len(derived) > 0 {
		d.DerivedFrom = json.RawMessage(derived)
	}
	utcPtr(d.DecidedAt)
	utcPtr(d.ExpiresAt)
	utcPtr(d.DeletedAt)
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	return d, nil
}

func (u *unit) getDecision(ctx context.Context, key string, forUpdate bool) (Decision, error) {
	k, e := u.resolveRef(ctx, "decisions", "", key)
	if e != nil {
		return Decision{}, e
	}
	q := "SELECT " + decisionCols + " FROM life.decisions WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	d, e := scanDecision(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "decisions", "", k)
	}
	return d, e
}

type decisionValues struct {
	question, kind, subject, origin string
	options                         []DecisionOption
	effect                          json.RawMessage
	expires                         *time.Time
	derived                         json.RawMessage
}

func (d DecisionData) values() (decisionValues, error) {
	v := decisionValues{question: strings.TrimSpace(d.Question), kind: d.Kind, subject: d.Subject, origin: d.Origin,
		options: d.Options, effect: d.Effect}
	if v.question == "" || len(v.question) > 500 {
		return v, invalid("question must be 1-500 characters")
	}
	if v.kind == "" {
		v.kind = "choice"
	}
	if !oneOf(v.kind, "choice", "change") {
		return v, invalid("kind must be choice or change")
	}
	if len(v.options) < 2 || len(v.options) > 10 {
		return v, invalid("a decision needs 2-10 options")
	}
	seen := map[string]bool{}
	for i, o := range v.options {
		o.ID, o.Label = strings.TrimSpace(o.ID), strings.TrimSpace(o.Label)
		if o.ID == "" || len(o.ID) > 40 || o.Label == "" || len(o.Label) > 200 {
			return v, invalid(fmt.Sprintf("options[%d] needs an id of 1-40 characters and a label of 1-200", i))
		}
		if seen[o.ID] {
			return v, invalid("option ids must be unique")
		}
		seen[o.ID] = true
		v.options[i] = o
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
	if d.ExpiresAt != nil {
		t, e := instant("expires_at", *d.ExpiresAt)
		if e != nil {
			return v, e
		}
		v.expires = &t
	}
	if v.kind == "change" {
		if len(v.effect) == 0 || string(v.effect) == "null" {
			return v, invalid("a change decision needs an effect: the batch applied when accept is chosen")
		}
		var plan BatchPlan
		if e := json.Unmarshal(v.effect, &plan); e != nil {
			return v, invalid("effect must be a batch plan: " + e.Error())
		}
		if e := checkBatch(plan); e != nil {
			return v, invalid("effect: " + e.Error())
		}
		if !seen["accept"] {
			return v, invalid("a change decision needs an option with id accept")
		}
	}
	derived, e := derivedFrom(d.DerivedFrom)
	if e != nil {
		return v, e
	}
	v.derived = derived
	return v, nil
}

func createDecision(ctx context.Context, u *unit, in CreateDecisionInput) (Decision, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Decision{}, e
	}
	v, e := in.DecisionData.values()
	if e != nil {
		return Decision{}, e
	}
	options, _ := json.Marshal(v.options)
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.decisions(id,subject,question,kind,options,expires_at,effect,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6::timestamptz,$7,$8,$9)`,
		key, v.subject, v.question, v.kind, options, v.expires, v.effect, v.origin, v.derived); e != nil {
		return Decision{}, e
	}
	d, e := u.getDecision(ctx, key, false)
	if e != nil {
		return d, e
	}
	return d, u.audit(ctx, "decision", key, nil, d)
}

func updateDecision(ctx context.Context, u *unit, in UpdateDecisionInput) (Decision, error) {
	old, e := u.getDecision(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.Status != "open" && old.Status != "deferred" {
		return old, conflict("A decision that is " + old.Status + " cannot be edited")
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the decision before editing it")
	}
	v, e := in.DecisionData.values()
	if e != nil {
		return old, e
	}
	options, _ := json.Marshal(v.options)
	if _, e = u.tx.Exec(ctx, `UPDATE life.decisions SET subject=$2,question=$3,kind=$4,options=$5,expires_at=$6::timestamptz,effect=$7,
		origin=$8,derived_from=$9,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.subject, v.question, v.kind, options, v.expires, v.effect, v.origin, v.derived); e != nil {
		return old, e
	}
	d, e := u.getDecision(ctx, old.ID, false)
	if e != nil {
		return d, e
	}
	return d, u.audit(ctx, "decision", d.ID, old, d)
}

// resolveDecision records the choice. A change decision applies its effect in the same transaction, so either the
// records exist and the decision is decided, or neither is true.
func resolveDecision(ctx context.Context, u *unit, s *Service, in ResolveDecisionInput) (Decision, error) {
	old, e := u.getDecision(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("The decision is deleted")
	}
	if old.Status == "decided" || old.Status == "expired" {
		return old, conflict("The decision is already " + old.Status)
	}
	now, e := u.now(ctx)
	if e != nil {
		return old, e
	}
	if old.ExpiresAt != nil && !old.ExpiresAt.After(now) {
		return old, conflict("The decision has expired")
	}
	found := false
	for _, o := range old.Options {
		if o.ID == in.Option {
			found = true
			break
		}
	}
	if !found {
		return old, invalid("option must be one of the decision's option ids")
	}
	decidedBy := in.DecidedBy
	if decidedBy == "" {
		decidedBy = "person"
	}
	if !oneOf(decidedBy, "person", "agent") {
		return old, invalid("decided_by must be person or agent")
	}
	if len(in.Rationale) > 2000 {
		return old, invalid("rationale must be at most 2000 characters")
	}
	// A change that came from memory is promoted only by the person: the agent's own judgement is not enough.
	if old.Kind == "change" && len(old.DerivedFrom) > 0 {
		if in.Confirmation == nil || !oneOf(in.Confirmation.Source, "client", "turn") || in.Confirmation.Ref == "" {
			return old, invalid("this change came from memory: resolving it needs confirmation {source: client or turn, ref: the action or turn id}")
		}
	}
	if old.Kind == "change" && in.Option == "accept" && len(old.Effect) > 0 {
		var plan BatchPlan
		if e = json.Unmarshal(old.Effect, &plan); e != nil {
			return old, invalid("the decision's effect is not a batch plan")
		}
		if _, e = s.runBatch(ctx, u, plan, in.IdempotencyKey+":effect", false); e != nil {
			return old, e
		}
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.decisions SET status='decided',chosen_option=$2,rationale=$3,decided_by=$4,decided_at=now(),
		version=version+1,updated_at=now() WHERE id=$1`, old.ID, in.Option, in.Rationale, decidedBy); e != nil {
		return old, e
	}
	d, e := u.getDecision(ctx, old.ID, false)
	if e != nil {
		return d, e
	}
	return d, u.audit(ctx, "decision", d.ID, old, d)
}

func deferDecision(ctx context.Context, u *unit, in DeferDecisionInput) (Decision, error) {
	old, e := u.getDecision(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.Status == "decided" || old.Status == "expired" {
		return old, conflict("The decision is already " + old.Status)
	}
	var until *time.Time
	if in.Until != "" {
		t, e := instant("until", in.Until)
		if e != nil {
			return old, e
		}
		until = &t
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.decisions SET status='deferred',expires_at=coalesce($2::timestamptz,expires_at),
		version=version+1,updated_at=now() WHERE id=$1`, old.ID, until); e != nil {
		return old, e
	}
	d, e := u.getDecision(ctx, old.ID, false)
	if e != nil {
		return d, e
	}
	return d, u.audit(ctx, "decision", d.ID, old, d)
}

func expireDecision(ctx context.Context, u *unit, in EditInput) (Decision, error) {
	old, e := u.getDecision(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.Status != "open" && old.Status != "deferred" {
		return old, conflict("The decision is already " + old.Status)
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.decisions SET status='expired',version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	d, e := u.getDecision(ctx, old.ID, false)
	if e != nil {
		return d, e
	}
	return d, u.audit(ctx, "decision", d.ID, old, d)
}

func listDecisions(ctx context.Context, u *unit, in ListDecisionsInput) (Page[Decision], error) {
	result := Page[Decision]{Items: []Decision{}}
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
	status := in.Status
	if status == "" {
		status = "open"
	}
	if status != "all" {
		if !oneOf(status, "open", "decided", "deferred", "expired") {
			return result, invalid("status must be open, decided, deferred, expired or all")
		}
		where = append(where, "status="+arg(status))
	}
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if in.Search != "" {
		p := arg(like(in.Search))
		where = append(where, "(question ILIKE "+p+" OR rationale ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.decisions WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+decisionCols+" FROM life.decisions WHERE "+clause+
		" ORDER BY expires_at ASC NULLS LAST, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		d, e := scanDecision(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		result.Items = append(result.Items, d)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}
