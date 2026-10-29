package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// A commitment is an obligation: something owed to a person, an institution or oneself. It is not a task. A commitment
// generates the action that discharges it, and every status change records an activity, so the history of an obligation
// survives edits to the commitment itself.
type Commitment struct {
	ID                 string          `json:"id"`
	Subject            string          `json:"subject" jsonschema:"person (the default) or agent"`
	Title              string          `json:"title"`
	Notes              string          `json:"notes"`
	Counterparty       string          `json:"counterparty" jsonschema:"who it is owed to, or an empty string"`
	Consequence        string          `json:"consequence" jsonschema:"what happens if it is missed, or an empty string"`
	DueAt              *time.Time      `json:"due_at" jsonschema:"the instant it is due, or null"`
	DueOn              *string         `json:"due_on" jsonschema:"the local date it is due, or null"`
	Recurrence         *Recurrence     `json:"recurrence" jsonschema:"how it repeats, or null"`
	AlertBeforeMinutes *int            `json:"alert_before_minutes" jsonschema:"tell the owner this many minutes before due_at, or null"`
	TimeZone           string          `json:"time_zone"`
	Status             string          `json:"status" jsonschema:"open, met, missed, renegotiated or waived"`
	Origin             string          `json:"origin" jsonschema:"person or agent: who authored it"`
	DerivedFrom        json.RawMessage `json:"derived_from,omitempty"`
	Version            int64           `json:"version"`
	DeletedAt          *time.Time      `json:"deleted_at"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type CommitmentData struct {
	Title              string          `json:"title" jsonschema:"1-200 characters"`
	Notes              string          `json:"notes,omitempty" jsonschema:"short free text, up to 2000 characters"`
	Counterparty       string          `json:"counterparty,omitempty" jsonschema:"who it is owed to, up to 200 characters"`
	Consequence        string          `json:"consequence,omitempty" jsonschema:"what happens if it is missed, up to 500 characters"`
	DueAt              *string         `json:"due_at,omitempty" jsonschema:"RFC 3339 instant it is due; give this or due_on, not both"`
	DueOn              *string         `json:"due_on,omitempty" jsonschema:"YYYY-MM-DD local date it is due; give this or due_at, not both"`
	Recurrence         *Recurrence     `json:"recurrence,omitempty" jsonschema:"optional repeat; requires due_at"`
	AlertBeforeMinutes *int            `json:"alert_before_minutes,omitempty" jsonschema:"tell the owner this many minutes before due_at (0 = at the time), up to 10080"`
	TimeZone           string          `json:"time_zone,omitempty" jsonschema:"IANA zone the person lives in; default UTC"`
	Subject            string          `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Origin             string          `json:"origin,omitempty" jsonschema:"person (default) or agent: who authored it"`
	DerivedFrom        json.RawMessage `json:"derived_from,omitempty" jsonschema:"when an agent promoted a memory candidate: {bank_id, record_id, snapshot}"`
}
type CreateCommitmentInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	CommitmentData
}
type UpdateCommitmentInput struct {
	EditInput
	CommitmentData
}
type CommitmentStatusInput struct {
	EditInput
	Note string `json:"note,omitempty" jsonschema:"why, in one line; recorded with the change"`
}
type ListCommitmentsInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Status  string `json:"status,omitempty" jsonschema:"open (default), met, missed, renegotiated, waived or all"`
}

const commitmentCols = `id::text,subject,title,notes,counterparty,consequence,due_at,due_on::text,recurrence,alert_before_minutes,time_zone,status,origin,derived_from,version,deleted_at,created_at,updated_at`

func scanCommitment(row scanner) (Commitment, error) {
	var c Commitment
	var rec, derived []byte
	err := row.Scan(&c.ID, &c.Subject, &c.Title, &c.Notes, &c.Counterparty, &c.Consequence, &c.DueAt, &c.DueOn, &rec,
		&c.AlertBeforeMinutes, &c.TimeZone, &c.Status, &c.Origin, &derived, &c.Version, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if len(rec) > 0 {
		c.Recurrence = new(Recurrence)
		if err = json.Unmarshal(rec, c.Recurrence); err != nil {
			return c, err
		}
	}
	if len(derived) > 0 {
		c.DerivedFrom = json.RawMessage(derived)
	}
	utcPtr(c.DueAt)
	utcPtr(c.DeletedAt)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, nil
}

func (u *unit) getCommitment(ctx context.Context, key string, forUpdate bool) (Commitment, error) {
	k, e := u.resolveRef(ctx, "commitments", "", key)
	if e != nil {
		return Commitment{}, e
	}
	q := "SELECT " + commitmentCols + " FROM life.commitments WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	c, e := scanCommitment(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "commitments", "", k)
	}
	return c, e
}

type commitmentValues struct {
	title, notes, counterparty, consequence, tz, subject, origin string
	dueAt, dueOn                                                 *string
	rec                                                          *Recurrence
	alert                                                        *int
	derived                                                      json.RawMessage
}

func (d CommitmentData) values() (commitmentValues, error) {
	v := commitmentValues{title: strings.TrimSpace(d.Title), notes: d.Notes, counterparty: d.Counterparty,
		consequence: d.Consequence, tz: d.TimeZone, subject: d.Subject, origin: d.Origin, dueAt: d.DueAt, dueOn: d.DueOn,
		rec: d.Recurrence, alert: d.AlertBeforeMinutes}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if len(v.notes) > 2000 {
		return v, invalid("notes must be at most 2000 characters")
	}
	if len(v.counterparty) > 200 || len(v.consequence) > 500 {
		return v, invalid("counterparty must be at most 200 characters and consequence at most 500")
	}
	if v.tz == "" {
		v.tz = "UTC"
	}
	loc, e := zone(v.tz)
	if e != nil {
		return v, e
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
	if v.dueAt != nil && v.dueOn != nil {
		return v, invalid("give due_at or due_on, not both")
	}
	if v.dueOn != nil {
		if _, e = day("due_on", *v.dueOn); e != nil {
			return v, e
		}
	}
	var at *time.Time
	if v.dueAt != nil {
		t, e := instant("due_at", *v.dueAt)
		if e != nil {
			return v, e
		}
		at = &t
	}
	if v.rec != nil {
		if at == nil {
			return v, invalid("recurrence requires due_at")
		}
		r := *v.rec
		if e = r.normalize(); e != nil {
			return v, e
		}
		v.rec = &r
	}
	if v.alert != nil {
		if at == nil {
			return v, invalid("alert_before_minutes requires due_at")
		}
		if *v.alert < 0 || *v.alert > 10080 {
			return v, invalid("alert_before_minutes must be 0-10080")
		}
		if v.rec != nil {
			if _, e = recurringAlert(*at, *v.alert, *v.rec, loc); e != nil {
				return v, e
			}
		}
	}
	derived, e := derivedFrom(d.DerivedFrom)
	if e != nil {
		return v, e
	}
	v.derived = derived
	return v, nil
}

func createCommitment(ctx context.Context, u *unit, in CreateCommitmentInput) (Commitment, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Commitment{}, e
	}
	v, e := in.CommitmentData.values()
	if e != nil {
		return Commitment{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.commitments(id,subject,title,notes,counterparty,consequence,due_at,due_on,recurrence,
		alert_before_minutes,time_zone,status,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6,$7::timestamptz,$8::date,$9,$10,$11,'open',$12,$13)`,
		key, v.subject, v.title, v.notes, v.counterparty, v.consequence, v.dueAt, v.dueOn, v.rec, v.alert, v.tz, v.origin, v.derived); e != nil {
		return Commitment{}, e
	}
	c, e := u.getCommitment(ctx, key, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "commitment", key, nil, c)
}

func updateCommitment(ctx context.Context, u *unit, in UpdateCommitmentInput) (Commitment, error) {
	old, e := u.getCommitment(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the commitment before editing it")
	}
	v, e := in.CommitmentData.values()
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.commitments SET subject=$2,title=$3,notes=$4,counterparty=$5,consequence=$6,due_at=$7::timestamptz,
		due_on=$8::date,recurrence=$9,alert_before_minutes=$10,time_zone=$11,origin=$12,derived_from=$13,version=version+1,updated_at=now()
		WHERE id=$1`, old.ID, v.subject, v.title, v.notes, v.counterparty, v.consequence, v.dueAt, v.dueOn, v.rec, v.alert, v.tz, v.origin, v.derived); e != nil {
		return old, e
	}
	c, e := u.getCommitment(ctx, old.ID, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "commitment", c.ID, old, c)
}

// setCommitmentStatus is the one transition: the status changes and an activity records who did it and why. `fulfils`
// links a met obligation to the act that discharged it; every other outcome `affects` it.
func setCommitmentStatus(ctx context.Context, u *unit, in CommitmentStatusInput, status string) (Commitment, error) {
	old, e := u.getCommitment(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("The commitment is deleted")
	}
	if old.Status == status {
		return old, conflict("Commitment is already " + status)
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.commitments SET status=$2,version=version+1,updated_at=now() WHERE id=$1`, old.ID, status); e != nil {
		return old, e
	}
	verb := map[string]string{"met": "Met", "missed": "Missed", "renegotiated": "Renegotiated", "waived": "Waived", "open": "Reopened"}[status]
	text := verb + ": " + old.Title
	if in.Note != "" {
		text += " — " + in.Note
	}
	now, e := u.now(ctx)
	if e != nil {
		return old, e
	}
	activity, e := u.insertActivity(ctx, "", old.Subject, "person", "occurrence", text, now, "person", "person", nil)
	if e != nil {
		return old, e
	}
	relation := "affects"
	if status == "met" {
		relation = "fulfils"
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.links(from_kind,from_id,relation,to_kind,to_id) VALUES('activity',$1,$2,'commitment',$3)
		ON CONFLICT DO NOTHING`, activity.ID, relation, old.ID); e != nil {
		return old, e
	}
	c, e := u.getCommitment(ctx, old.ID, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "commitment", c.ID, old, c)
}

func lifecycleCommitment(ctx context.Context, u *unit, in EditInput, restore bool) (Commitment, error) {
	old, e := u.getCommitment(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Commitment is not deleted")
		}
		return old, conflict("Commitment is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.commitments SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	c, e := u.getCommitment(ctx, old.ID, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "commitment", c.ID, old, c)
}

func listCommitments(ctx context.Context, u *unit, in ListCommitmentsInput) (Page[Commitment], error) {
	result := Page[Commitment]{Items: []Commitment{}}
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
		if !oneOf(status, "open", "met", "missed", "renegotiated", "waived") {
			return result, invalid("status must be open, met, missed, renegotiated, waived or all")
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
		where = append(where, "(title ILIKE "+p+" OR notes ILIKE "+p+" OR counterparty ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.commitments WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+commitmentCols+" FROM life.commitments WHERE "+clause+
		" ORDER BY due_at ASC NULLS LAST, due_on ASC NULLS LAST, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		c, e := scanCommitment(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		result.Items = append(result.Items, c)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}
