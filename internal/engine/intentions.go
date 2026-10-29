package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// An intention is a standing want: achieve ("run a 10k"), maintain ("keep weekends free") or experience ("see the
// northern lights"). It may have a target, an end date, both or neither. Progress is read from the tasks linked to it
// through the links table, never stored. `origin` says who authored it; `derived_from` names the memory candidate an
// agent promoted, when there is one.
type Intention struct {
	ID          string          `json:"id"`
	Subject     string          `json:"subject" jsonschema:"person (the default) or agent"`
	Title       string          `json:"title"`
	Kind        string          `json:"kind" jsonschema:"achieve, maintain or experience"`
	Notes       string          `json:"notes"`
	EndsOn      *string         `json:"ends_on" jsonschema:"local date it finishes by, or null"`
	Target      *Target         `json:"target" jsonschema:"the number to reach, or null"`
	Status      string          `json:"status" jsonschema:"active, satisfied or abandoned"`
	Origin      string          `json:"origin" jsonschema:"person or agent: who authored it"`
	DerivedFrom json.RawMessage `json:"derived_from,omitempty" jsonschema:"the memory candidate it came from, when an agent promoted one"`
	Progress    Progress        `json:"progress" jsonschema:"read from the tasks linked to it"`
	Version     int64           `json:"version"`
	DeletedAt   *time.Time      `json:"deleted_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type Target struct {
	Value float64 `json:"value" jsonschema:"the number to reach, such as 10"`
	Unit  string  `json:"unit" jsonschema:"what it counts, such as km or kg, 1-24 characters"`
}

type IntentionData struct {
	Title       string          `json:"title" jsonschema:"1-200 characters, in the person's words"`
	Kind        string          `json:"kind,omitempty" jsonschema:"achieve (default), maintain or experience"`
	Notes       string          `json:"notes,omitempty" jsonschema:"short free text, up to 2000 characters"`
	EndsOn      *string         `json:"ends_on,omitempty" jsonschema:"YYYY-MM-DD it finishes by; omit for open-ended"`
	Target      *Target         `json:"target,omitempty" jsonschema:"optional number to reach"`
	Subject     string          `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Status      string          `json:"status,omitempty" jsonschema:"active (default), satisfied or abandoned"`
	Origin      string          `json:"origin,omitempty" jsonschema:"person (default) or agent: who authored it"`
	DerivedFrom json.RawMessage `json:"derived_from,omitempty" jsonschema:"when an agent promoted a memory candidate: {bank_id, record_id, snapshot}"`
}
type CreateIntentionInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID so a later operation in the same batch can refer to it"`
	IntentionData
}
type UpdateIntentionInput struct {
	EditInput
	IntentionData
}
type ListIntentionsInput struct {
	ListInput
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
	Status  string `json:"status,omitempty" jsonschema:"active (default), satisfied, abandoned or all"`
}

const intentionCols = `id::text,subject,title,kind,notes,ends_on::text,target,status,origin,derived_from,version,deleted_at,created_at,updated_at`

func scanIntention(row scanner) (Intention, error) {
	var it Intention
	var target, derived []byte
	err := row.Scan(&it.ID, &it.Subject, &it.Title, &it.Kind, &it.Notes, &it.EndsOn, &target, &it.Status, &it.Origin, &derived,
		&it.Version, &it.DeletedAt, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return it, err
	}
	if len(target) > 0 {
		it.Target = new(Target)
		if err = json.Unmarshal(target, it.Target); err != nil {
			return it, err
		}
	}
	if len(derived) > 0 {
		it.DerivedFrom = json.RawMessage(derived)
	}
	utcPtr(it.DeletedAt)
	it.CreatedAt, it.UpdatedAt = it.CreatedAt.UTC(), it.UpdatedAt.UTC()
	return it, nil
}

// intentionProgress counts the one-off tasks linked to the intention that are done, and the linked tasks in total. A
// repeating task counts once until the cutover replaces occurrences with activities; tasks_history reads its detail.
func (u *unit) withIntentionProgress(ctx context.Context, it Intention) (Intention, error) {
	err := u.tx.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE t.done_at IS NOT NULL), count(*)
		FROM life.links l JOIN life.tasks t ON t.id=l.from_id
		WHERE l.relation='serves' AND l.to_kind='intention' AND l.to_id=$1
		  AND l.from_kind='task' AND t.deleted_at IS NULL`, it.ID).Scan(&it.Progress.Done, &it.Progress.Total)
	return it, err
}

func (u *unit) getIntention(ctx context.Context, key string, forUpdate bool) (Intention, error) {
	k, e := u.resolveRef(ctx, "intentions", "", key)
	if e != nil {
		return Intention{}, e
	}
	q := "SELECT " + intentionCols + " FROM life.intentions WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	it, e := scanIntention(u.tx.QueryRow(ctx, q, k))
	if isNoRows(e) {
		e = u.missing(ctx, "intentions", "", k)
	}
	if e != nil {
		return it, e
	}
	return u.withIntentionProgress(ctx, it)
}

type intentionValues struct {
	title, kind, notes, subject, status, origin string
	endsOn                                      *string
	target                                      *Target
	derived                                     json.RawMessage
}

func (d IntentionData) values(creating bool) (intentionValues, error) {
	v := intentionValues{title: strings.TrimSpace(d.Title), kind: d.Kind, notes: d.Notes, subject: d.Subject,
		status: d.Status, origin: d.Origin, endsOn: d.EndsOn, target: d.Target}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if len(v.notes) > 2000 {
		return v, invalid("notes must be at most 2000 characters")
	}
	if v.kind == "" {
		v.kind = "achieve"
	}
	if !oneOf(v.kind, "achieve", "maintain", "experience") {
		return v, invalid("kind must be achieve, maintain or experience")
	}
	if v.subject == "" {
		v.subject = "person"
	}
	if !oneOf(v.subject, "person", "agent") {
		return v, invalid("subject must be person or agent")
	}
	if v.status == "" {
		v.status = "active"
	}
	if !oneOf(v.status, "active", "satisfied", "abandoned") {
		return v, invalid("status must be active, satisfied or abandoned")
	}
	if creating && v.status != "active" {
		return v, invalid("a new intention starts active; create it, then update its status")
	}
	if v.origin == "" {
		v.origin = "person"
	}
	if !oneOf(v.origin, "person", "agent") {
		return v, invalid("origin must be person or agent")
	}
	if v.endsOn != nil {
		if _, e := day("ends_on", *v.endsOn); e != nil {
			return v, e
		}
	}
	if v.target != nil {
		unit := strings.TrimSpace(v.target.Unit)
		if unit == "" || len(unit) > 24 {
			return v, invalid("target.unit must be 1-24 characters")
		}
		v.target = &Target{Value: v.target.Value, Unit: unit}
	}
	derived, e := derivedFrom(d.DerivedFrom)
	if e != nil {
		return v, e
	}
	v.derived = derived
	return v, nil
}

func derivedFrom(v json.RawMessage) (json.RawMessage, error) {
	if len(v) == 0 || string(v) == "null" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(v, &obj); err != nil || obj == nil {
		return nil, invalid("derived_from must be a JSON object")
	}
	return v, nil
}

func createIntention(ctx context.Context, u *unit, in CreateIntentionInput) (Intention, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Intention{}, e
	}
	v, e := in.IntentionData.values(true)
	if e != nil {
		return Intention{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.intentions(id,subject,title,kind,notes,ends_on,target,status,origin,derived_from)
		VALUES($1,$2,$3,$4,$5,$6::date,$7,$8,$9,$10)`, key, v.subject, v.title, v.kind, v.notes, v.endsOn, v.target, v.status, v.origin, v.derived); e != nil {
		return Intention{}, e
	}
	it, e := u.getIntention(ctx, key, false)
	if e != nil {
		return it, e
	}
	return it, u.audit(ctx, "intention", key, nil, it)
}

func updateIntention(ctx context.Context, u *unit, in UpdateIntentionInput) (Intention, error) {
	old, e := u.getIntention(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the intention before editing it")
	}
	v, e := in.IntentionData.values(false)
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.intentions SET subject=$2,title=$3,kind=$4,notes=$5,ends_on=$6::date,target=$7,status=$8,origin=$9,
		derived_from=$10,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.subject, v.title, v.kind, v.notes, v.endsOn, v.target, v.status, v.origin, v.derived); e != nil {
		return old, e
	}
	it, e := u.getIntention(ctx, old.ID, false)
	if e != nil {
		return it, e
	}
	return it, u.audit(ctx, "intention", it.ID, old, it)
}

func lifecycleIntention(ctx context.Context, u *unit, in EditInput, restore bool) (Intention, error) {
	old, e := u.getIntention(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Intention is not archived")
		}
		return old, conflict("Intention is already archived")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.intentions SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	it, e := u.getIntention(ctx, old.ID, false)
	if e != nil {
		return it, e
	}
	return it, u.audit(ctx, "intention", it.ID, old, it)
}

func listIntentions(ctx context.Context, u *unit, in ListIntentionsInput) (Page[Intention], error) {
	result := Page[Intention]{Items: []Intention{}}
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
		status = "active"
	}
	if status != "all" {
		if !oneOf(status, "active", "satisfied", "abandoned") {
			return result, invalid("status must be active, satisfied, abandoned or all")
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
		where = append(where, "(title ILIKE "+p+" OR notes ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.intentions WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+intentionCols+" FROM life.intentions WHERE "+clause+
		" ORDER BY ends_on ASC NULLS LAST, created_at, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	var items []Intention
	for rows.Next() {
		it, e := scanIntention(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		items = append(items, it)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	for _, it := range items {
		if it, e = u.withIntentionProgress(ctx, it); e != nil {
			return result, e
		}
		result.Items = append(result.Items, it)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}
