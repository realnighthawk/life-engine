package engine

import (
	"context"
	"sort"
	"strings"
	"time"
)

type Note struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Body        string       `json:"body" jsonschema:"markdown"`
	Tags        []string     `json:"tags"`
	GoalID      string       `json:"goal_id,omitempty"`
	Pinned      bool         `json:"pinned"`
	Attachments []Attachment `json:"attachments"`
	Version     int64        `json:"version"`
	DeletedAt   *time.Time   `json:"deleted_at"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}
type NoteData struct {
	Title  string   `json:"title" jsonschema:"1-200 characters"`
	Body   string   `json:"body,omitempty" jsonschema:"markdown, up to 50000 characters; lists and logs the person keeps live here"`
	Tags   []string `json:"tags,omitempty" jsonschema:"up to 20 short labels"`
	GoalID string   `json:"goal_id,omitempty"`
	Pinned bool     `json:"pinned,omitempty"`
}
type CreateNoteInput struct {
	Meta
	ID string `json:"id,omitempty"`
	NoteData
}
type UpdateNoteInput struct {
	EditInput
	NoteData
}
type ListNotesInput struct {
	ListInput
	Tag    string `json:"tag,omitempty"`
	GoalID string `json:"goal_id,omitempty"`
	Pinned *bool  `json:"pinned,omitempty"`
}

const noteCols = `id::text,title,body,tags,coalesce(goal_id::text,''),pinned,version,deleted_at,created_at,updated_at`

func scanNote(row scanner) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Title, &n.Body, &n.Tags, &n.GoalID, &n.Pinned, &n.Version, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt)
	n.CreatedAt, n.UpdatedAt = n.CreatedAt.UTC(), n.UpdatedAt.UTC()
	utcPtr(n.DeletedAt)
	if n.Tags == nil {
		n.Tags = []string{}
	}
	n.Attachments = []Attachment{}
	return n, err
}

func (u *unit) getNote(ctx context.Context, key string, forUpdate bool) (Note, error) {
	k, e := id(key)
	if e != nil {
		return Note{}, e
	}
	q := "SELECT " + noteCols + " FROM life.notes WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	n, e := scanNote(u.tx.QueryRow(ctx, q, k))
	if e != nil {
		return n, e
	}
	n.Attachments, e = u.attachmentsOf(ctx, "note", n.ID)
	return n, e
}

func (u *unit) noteValues(ctx context.Context, d NoteData) (title string, tags []string, goal string, err error) {
	title = strings.TrimSpace(d.Title)
	if title == "" || len(title) > 200 {
		return "", nil, "", invalid("title must be 1-200 characters")
	}
	if len(d.Body) > 50000 {
		return "", nil, "", invalid("body must be at most 50000 characters")
	}
	seen := map[string]bool{}
	tags = []string{}
	for _, t := range d.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || len(t) > 40 {
			return "", nil, "", invalid("each tag must be 1-40 characters")
		}
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	if len(tags) > 20 {
		return "", nil, "", invalid("at most 20 tags")
	}
	sort.Strings(tags)
	goal, err = u.refExists(ctx, "goals", d.GoalID)
	return
}

func createNote(ctx context.Context, u *unit, in CreateNoteInput) (Note, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Note{}, e
	}
	title, tags, goal, e := u.noteValues(ctx, in.NoteData)
	if e != nil {
		return Note{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.notes(id,title,body,tags,goal_id,pinned) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6)`, key, title, in.Body, tags, goal, in.Pinned); e != nil {
		return Note{}, e
	}
	n, e := u.getNote(ctx, key, false)
	if e != nil {
		return n, e
	}
	return n, u.audit(ctx, "note", key, nil, n)
}

func (u *unit) noteChanged(ctx context.Context, old Note) (Note, error) {
	n, e := u.getNote(ctx, old.ID, false)
	if e != nil {
		return n, e
	}
	return n, u.audit(ctx, "note", n.ID, old, n)
}

func updateNote(ctx context.Context, u *unit, in UpdateNoteInput) (Note, error) {
	old, e := u.getNote(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the note before editing it")
	}
	title, tags, goal, e := u.noteValues(ctx, in.NoteData)
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.notes SET title=$2,body=$3,tags=$4,goal_id=NULLIF($5,'')::uuid,pinned=$6,version=version+1,updated_at=now() WHERE id=$1`, old.ID, title, in.Body, tags, goal, in.Pinned); e != nil {
		return old, e
	}
	return u.noteChanged(ctx, old)
}

func lifecycleNote(ctx context.Context, u *unit, in EditInput, restore bool) (Note, error) {
	old, e := u.getNote(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Note is not deleted")
		}
		return old, conflict("Note is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.notes SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.noteChanged(ctx, old)
}

func listNotes(ctx context.Context, u *unit, in ListNotesInput) (Page[Note], error) {
	result := Page[Note]{Items: []Note{}}
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
		where = append(where, textMatch("(title || ' ' || body)", arg, in.Search))
	}
	if in.Tag != "" {
		where = append(where, arg(strings.ToLower(strings.TrimSpace(in.Tag)))+" = ANY(tags)")
	}
	if in.GoalID != "" {
		g, e := id(in.GoalID)
		if e != nil {
			return result, e
		}
		where = append(where, "goal_id="+arg(g))
	}
	if in.Pinned != nil {
		where = append(where, "pinned="+arg(*in.Pinned))
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.notes WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+noteCols+" FROM life.notes WHERE "+clause+" ORDER BY pinned DESC, updated_at DESC, id LIMIT "+arg(limit)+" OFFSET "+arg(offset), args...)
	if e != nil {
		return result, e
	}
	notes, e := collect(rows, scanNote)
	if e != nil {
		return result, e
	}
	for i := range notes {
		if notes[i].Attachments, e = u.attachmentsOf(ctx, "note", notes[i].ID); e != nil {
			return result, e
		}
	}
	if notes != nil {
		result.Items = notes
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, nil
}
