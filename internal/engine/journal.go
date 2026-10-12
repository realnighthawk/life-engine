package engine

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type JournalEntry struct {
	ID          string       `json:"id"`
	EntryAt     time.Time    `json:"entry_at"`
	Text        string       `json:"text"`
	GoalIDs     []string     `json:"goal_ids" jsonschema:"goals this entry connects to"`
	Tomorrow    *string      `json:"tomorrow" jsonschema:"one change for tomorrow, if the person chose one"`
	Source      string       `json:"source" jsonschema:"user, agent or day_close"`
	SessionID   string       `json:"session_id,omitempty"`
	TurnSeq     *int64       `json:"turn_seq"`
	Attachments []Attachment `json:"attachments"`
	Version     int64        `json:"version"`
	DeletedAt   *time.Time   `json:"deleted_at"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}
type JournalData struct {
	Text      string   `json:"text" jsonschema:"what happened and how it felt, in the person's words, 1-20000 characters"`
	EntryAt   *string  `json:"entry_at,omitempty" jsonschema:"RFC 3339; default now"`
	GoalIDs   []string `json:"goal_ids,omitempty" jsonschema:"goals the entry relates to"`
	Tomorrow  string   `json:"tomorrow,omitempty" jsonschema:"one adjustment for tomorrow, up to 500 characters"`
	Source    string   `json:"source,omitempty" jsonschema:"user (default) or agent"`
	SessionID string   `json:"session_id,omitempty"`
	TurnSeq   *int64   `json:"turn_seq,omitempty"`
}
type CreateJournalInput struct {
	Meta
	ID string `json:"id,omitempty"`
	JournalData
}
type UpdateJournalInput struct {
	EditInput
	JournalData
}
type ListJournalInput struct {
	ListInput
	From   string `json:"from,omitempty" jsonschema:"RFC 3339; entries at or after"`
	To     string `json:"to,omitempty" jsonschema:"RFC 3339; entries before"`
	GoalID string `json:"goal_id,omitempty"`
}

const journalCols = `id::text,entry_at,text,goal_ids::text[],tomorrow,source,coalesce(session_id,''),turn_seq,version,deleted_at,created_at,updated_at`

func scanJournal(row scanner) (JournalEntry, error) {
	var j JournalEntry
	err := row.Scan(&j.ID, &j.EntryAt, &j.Text, &j.GoalIDs, &j.Tomorrow, &j.Source, &j.SessionID, &j.TurnSeq, &j.Version, &j.DeletedAt, &j.CreatedAt, &j.UpdatedAt)
	j.EntryAt, j.CreatedAt, j.UpdatedAt = j.EntryAt.UTC(), j.CreatedAt.UTC(), j.UpdatedAt.UTC()
	utcPtr(j.DeletedAt)
	if j.GoalIDs == nil {
		j.GoalIDs = []string{}
	}
	j.Attachments = []Attachment{}
	return j, err
}

func (u *unit) getJournal(ctx context.Context, key string, forUpdate bool) (JournalEntry, error) {
	k, e := id(key)
	if e != nil {
		return JournalEntry{}, e
	}
	q := "SELECT " + journalCols + " FROM life.journal_entries WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	j, e := scanJournal(u.tx.QueryRow(ctx, q, k))
	if e != nil {
		return j, e
	}
	j.Attachments, e = u.attachmentsOf(ctx, "journal_entry", j.ID)
	return j, e
}

type journalValues struct {
	text, source, session string
	at                    time.Time
	goals                 []string
	tomorrow              *string
	turn                  *int64
}

func (u *unit) journalValues(ctx context.Context, d JournalData) (journalValues, error) {
	v := journalValues{text: strings.TrimSpace(d.Text), source: d.Source, session: d.SessionID, turn: d.TurnSeq}
	if v.text == "" || len(v.text) > 20000 {
		return v, invalid("text must be 1-20000 characters")
	}
	if v.source == "" {
		v.source = "user"
	}
	if !oneOf(v.source, "user", "agent") {
		return v, invalid("source must be user or agent")
	}
	if d.EntryAt != nil {
		t, e := instant("entry_at", *d.EntryAt)
		if e != nil {
			return v, e
		}
		v.at = t
	} else {
		now, e := u.now(ctx)
		if e != nil {
			return v, e
		}
		v.at = now
	}
	if t := strings.TrimSpace(d.Tomorrow); t != "" {
		if len(t) > 500 {
			return v, invalid("tomorrow must be at most 500 characters")
		}
		v.tomorrow = &t
	}
	if len(d.GoalIDs) > 20 {
		return v, invalid("at most 20 goal_ids")
	}
	v.goals = []string{}
	for _, g := range d.GoalIDs {
		k, e := u.refExists(ctx, "goals", g)
		if e != nil {
			return v, e
		}
		v.goals = append(v.goals, k)
	}
	return v, nil
}

func createJournal(ctx context.Context, u *unit, in CreateJournalInput) (JournalEntry, error) {
	key, e := createID(in.ID)
	if e != nil {
		return JournalEntry{}, e
	}
	v, e := u.journalValues(ctx, in.JournalData)
	if e != nil {
		return JournalEntry{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.journal_entries(id,entry_at,text,goal_ids,tomorrow,source,session_id,turn_seq) VALUES($1,$2,$3,$4::uuid[],$5,$6,NULLIF($7,''),$8)`,
		key, v.at, v.text, v.goals, v.tomorrow, v.source, v.session, v.turn); e != nil {
		return JournalEntry{}, e
	}
	j, e := u.getJournal(ctx, key, false)
	if e != nil {
		return j, e
	}
	return j, u.audit(ctx, "journal_entry", key, nil, j)
}

func (u *unit) journalChanged(ctx context.Context, old JournalEntry) (JournalEntry, error) {
	j, e := u.getJournal(ctx, old.ID, false)
	if e != nil {
		return j, e
	}
	return j, u.audit(ctx, "journal_entry", j.ID, old, j)
}

func updateJournal(ctx context.Context, u *unit, in UpdateJournalInput) (JournalEntry, error) {
	old, e := u.getJournal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the entry before editing it")
	}
	v, e := u.journalValues(ctx, in.JournalData)
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.journal_entries SET entry_at=$2,text=$3,goal_ids=$4::uuid[],tomorrow=$5,source=$6,session_id=NULLIF($7,''),turn_seq=$8,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.at, v.text, v.goals, v.tomorrow, v.source, v.session, v.turn); e != nil {
		return old, e
	}
	return u.journalChanged(ctx, old)
}

func lifecycleJournal(ctx context.Context, u *unit, in EditInput, restore bool) (JournalEntry, error) {
	old, e := u.getJournal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Entry is not deleted")
		}
		return old, conflict("Entry is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.journal_entries SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.journalChanged(ctx, old)
}

// textMatch builds the search clause: full-text on the words typed, or the literal text as a fallback (names, partial words).
func textMatch(column string, arg func(any) string, search string) string {
	q := arg(search)
	l := arg(like(search))
	return "(tsv @@ websearch_to_tsquery('english', " + q + ") OR " + column + " ILIKE " + l + ")"
}

func listJournal(ctx context.Context, u *unit, in ListJournalInput) (Page[JournalEntry], error) {
	result := Page[JournalEntry]{Items: []JournalEntry{}}
	limit, offset, state, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	where, args := []string{"true"}, []any{}
	arg := func(v any) string { args = append(args, v); return "$" + itoa(len(args)) }
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if in.Search != "" {
		where = append(where, textMatch("text", arg, in.Search))
	}
	for _, b := range []struct{ v, op string }{{in.From, ">="}, {in.To, "<"}} {
		if b.v == "" {
			continue
		}
		t, e := instant("from/to", b.v)
		if e != nil {
			return result, e
		}
		where = append(where, "entry_at "+b.op+" "+arg(t))
	}
	if in.GoalID != "" {
		g, e := id(in.GoalID)
		if e != nil {
			return result, e
		}
		where = append(where, arg(g)+"::uuid = ANY(goal_ids)")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.journal_entries WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+journalCols+" FROM life.journal_entries WHERE "+clause+" ORDER BY entry_at DESC, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	entries, e := collect(rows, scanJournal)
	if e != nil {
		return result, e
	}
	for i := range entries {
		if entries[i].Attachments, e = u.attachmentsOf(ctx, "journal_entry", entries[i].ID); e != nil {
			return result, e
		}
	}
	if entries != nil {
		result.Items = entries
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}

// collect reads every row then closes the cursor, so the caller can run further queries on the same transaction.
func collect[T any](rows pgx.Rows, scan func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
