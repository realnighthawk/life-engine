package engine

import "context"

// Attention is the deterministic answer to "what needs the person now": obligations that are due, decisions that are
// open, and tasks that are overdue. It is a query, never a table, and the gateway decides how to deliver it.
type Attention struct {
	Commitments []Commitment `json:"commitments" jsonschema:"open obligations due today or earlier"`
	Decisions   []Decision   `json:"decisions" jsonschema:"decisions that are open or deferred and not expired"`
	Tasks       []Task       `json:"tasks" jsonschema:"open tasks whose time has passed"`
}

func attentionGet(ctx context.Context, u *unit, _ struct{}) (Attention, error) {
	out := Attention{Commitments: []Commitment{}, Decisions: []Decision{}, Tasks: []Task{}}
	today, _, _, e := u.today(ctx)
	if e != nil {
		return out, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+commitmentCols+` FROM life.commitments
		WHERE subject='person' AND deleted_at IS NULL AND status='open'
		  AND ((due_at IS NOT NULL AND due_at <= now()) OR (due_on IS NOT NULL AND due_on <= $1::date))
		ORDER BY due_at ASC NULLS LAST, due_on ASC NULLS LAST, id LIMIT 50`, today)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		c, e := scanCommitment(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Commitments = append(out.Commitments, c)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	rows, e = u.tx.Query(ctx, "SELECT "+decisionCols+` FROM life.decisions
		WHERE subject='person' AND deleted_at IS NULL AND status IN ('open','deferred')
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY expires_at ASC NULLS LAST, created_at, id LIMIT 50`)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		d, e := scanDecision(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Decisions = append(out.Decisions, d)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	rows, e = u.tx.Query(ctx, "SELECT "+taskCols+` FROM life.tasks
		WHERE subject='person' AND deleted_at IS NULL AND done_at IS NULL AND at IS NOT NULL AND at <= now()
		ORDER BY at, id LIMIT 50`)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		it, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Tasks = append(out.Tasks, it)
	}
	rows.Close()
	return out, rows.Err()
}
