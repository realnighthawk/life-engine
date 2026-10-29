package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// An agent copies 36-character ids by hand and sometimes drops or alters a character. A record is therefore also
// found by an unambiguous id prefix, and a miss says which records are closest. Nothing is ever picked on a guess:
// the caller retries with the id the error names.
var idPrefix = regexp.MustCompile(`^[0-9a-fA-F-]{8,35}$`)

// refRow is one candidate record.
type refRow struct{ id, title string }

// resolveRef returns the full id for key, which is a whole UUID or a unique prefix of one. table names the record family
// (tasks, events, intentions, commitments, decisions, states or activities). A whole UUID is returned as is and left to
// the caller's lookup, which reports a miss with suggestions through missing.
func (u *unit) resolveRef(ctx context.Context, table, kind, key string) (string, error) {
	if _, err := uuid.Parse(key); err == nil {
		return id(key)
	}
	if !idPrefix.MatchString(key) {
		return "", invalid("id must be a UUID")
	}
	rows, err := u.candidates(ctx, table, kind, strings.ToLower(key)+"%")
	if err != nil {
		return "", err
	}
	switch len(rows) {
	case 1:
		return rows[0].id, nil
	case 0:
		return "", u.missing(ctx, table, kind, key)
	}
	return "", conflict("Id prefix " + key + " matches more than one record: " + listRefs(rows))
}

// missing is the not-found error for key, naming the records whose ids are nearest to it.
func (u *unit) missing(ctx context.Context, table, kind, key string) error {
	rows, err := u.candidates(ctx, table, kind, "%")
	if err != nil {
		return err
	}
	var near []refRow
	for _, r := range rows {
		if idDistance(r.id, strings.ToLower(key)) <= 4 {
			near = append(near, r)
		}
	}
	if len(near) == 0 {
		return notFound()
	}
	if len(near) > 5 {
		near = near[:5]
	}
	return &Error{"not_found", "No record has id " + key + ". Closest: " + listRefs(near) + ". Retry with the exact id."}
}

func (u *unit) candidates(ctx context.Context, table, kind, like string) ([]refRow, error) {
	var q string
	var args []any
	switch table {
	case "tasks":
		q, args = "SELECT id::text, title FROM life.tasks WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	case "events":
		q, args = "SELECT id::text, title FROM life.events WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	case "intentions":
		q, args = "SELECT id::text, title FROM life.intentions WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	case "commitments":
		q, args = "SELECT id::text, title FROM life.commitments WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	case "decisions":
		q, args = "SELECT id::text, question FROM life.decisions WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	case "states":
		q, args = "SELECT id::text, topic || ': ' || predicate FROM life.states WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	case "activities":
		q, args = "SELECT id::text, text FROM life.activities WHERE id::text LIKE $1 LIMIT 200", []any{like}
	default:
		q, args = "SELECT id::text, title FROM life.tasks WHERE id::text LIKE $1 AND deleted_at IS NULL LIMIT 200", []any{like}
	}
	rs, err := u.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []refRow
	for rs.Next() {
		var r refRow
		if err = rs.Scan(&r.id, &r.title); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func listRefs(rows []refRow) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = fmt.Sprintf("%s (%s)", r.id, r.title)
	}
	return strings.Join(parts, "; ")
}

// idDistance is the edit distance between two ids (insertions, deletions and substitutions).
func idDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
