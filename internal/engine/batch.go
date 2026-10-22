package engine

import (
	"context"
	"encoding/json"
	"fmt"
)

// batchable are the operations a batch may run: the creates a plan is made of. A task or event links to a goal in the same
// batch by that goal's chosen id.
var batchable = map[string]bool{"goals_create": true, "tasks_create": true, "calendar_events_create": true}

type BatchOp struct {
	Operation string          `json:"operation" jsonschema:"goals_create, tasks_create or calendar_events_create"`
	Input     json.RawMessage `json:"input" jsonschema:"that operation's own input; leave out idempotency_key, the batch sets it. Give each new record an id so later operations can refer to it"`
}
type BatchPlan struct {
	Operations []BatchOp `json:"operations" jsonschema:"1-50 operations, run in this order; a later one may refer to a record an earlier one creates"`
}
type BatchApplyInput struct {
	Meta
	BatchPlan
}
type BatchStep struct {
	Operation string          `json:"operation"`
	Result    json.RawMessage `json:"result"`
}
type BatchResult struct {
	DryRun bool        `json:"dry_run"`
	Steps  []BatchStep `json:"steps"`
}

// runBatch runs a plan inside a savepoint of the caller's transaction, so it either lands whole or not at all, and a
// dry run can execute every operation for real and then undo it. Each step is the ordinary operation, so a batch can
// never accept what the single operation would refuse.
func (s *Service) runBatch(ctx context.Context, u *unit, plan BatchPlan, key string, dryRun bool) (BatchResult, error) {
	res := BatchResult{DryRun: dryRun, Steps: []BatchStep{}}
	if n := len(plan.Operations); n < 1 || n > 50 {
		return res, invalid("a batch holds 1-50 operations")
	}
	sp, e := u.tx.Begin(ctx)
	if e != nil {
		return res, e
	}
	defer sp.Rollback(context.Background()) // a no-op once committed
	inner := *u
	inner.tx = sp
	for i, o := range plan.Operations {
		op := s.operations[o.Operation]
		if op == nil || !batchable[o.Operation] {
			return res, invalid(fmt.Sprintf("operations[%d]: %q cannot be part of a batch", i, o.Operation))
		}
		raw, e := withKey(o.Input, fmt.Sprintf("%s:%d", key, i))
		if e != nil {
			return res, failedAt(i, o.Operation, e)
		}
		input, _, e := op.decode(raw)
		if e != nil {
			return res, failedAt(i, o.Operation, e)
		}
		out, e := op.run(ctx, &inner, input)
		if e != nil {
			return res, failedAt(i, o.Operation, e)
		}
		body, e := json.Marshal(out)
		if e != nil {
			return res, e
		}
		res.Steps = append(res.Steps, BatchStep{Operation: o.Operation, Result: body})
	}
	if dryRun {
		return res, nil
	}
	return res, sp.Commit(ctx)
}

// withKey sets the step's idempotency key; the batch's own key is what makes a retry safe.
func withKey(raw json.RawMessage, key string) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if e := json.Unmarshal(raw, &fields); e != nil || fields == nil {
			return nil, invalid("input must be a JSON object")
		}
	}
	fields["idempotency_key"], _ = json.Marshal(key)
	return json.Marshal(fields)
}

// failedAt names the failing step, keeping the error's code so the caller can still tell invalid from conflict.
func failedAt(i int, name string, e error) error {
	if pe, ok := e.(*Error); ok {
		return &Error{pe.Code, fmt.Sprintf("operations[%d] %s: %s", i, name, pe.Message)}
	}
	return e
}

func (s *Service) batchApply(ctx context.Context, u *unit, in BatchApplyInput) (BatchResult, error) {
	return s.runBatch(ctx, u, in.BatchPlan, in.IdempotencyKey, false)
}

func (s *Service) batchDryRun(ctx context.Context, u *unit, in BatchPlan) (BatchResult, error) {
	return s.runBatch(ctx, u, in, "dry-run", true)
}

// inverse is how each record a batch creates is taken away again, and where to see if it already is.
var inverse = map[string]struct{ op, table, removed string }{
	"tasks_create":           {"tasks_delete", "items", "deleted_at"},
	"calendar_events_create": {"calendar_events_delete", "items", "deleted_at"},
	"goals_create":           {"goals_archive", "goals", "deleted_at"},
}

type BatchUndoInput struct {
	Meta
	BatchKey string `json:"batch_key" jsonschema:"the idempotency_key the batch_apply was called with"`
}
type UndoStep struct {
	Operation string `json:"operation"`
	ID        string `json:"id"`
	Status    string `json:"status" jsonschema:"removed, already_removed or not_undone"`
}
type BatchUndoResult struct {
	Steps []UndoStep `json:"steps"`
}

// batchUndo reverses a recorded batch_apply: the stored response lists what it created, and each record is removed by
// the ordinary operation, newest first. Whatever was edited since is removed as it now stands.
func (s *Service) batchUndo(ctx context.Context, u *unit, in BatchUndoInput) (BatchUndoResult, error) {
	res := BatchUndoResult{Steps: []UndoStep{}}
	var name string
	var stored []byte
	if e := u.tx.QueryRow(ctx, "SELECT operation,response FROM life.requests WHERE key=$1", in.BatchKey).Scan(&name, &stored); e != nil {
		return res, e
	}
	if name != "batch_apply" {
		return res, invalid("batch_key does not name a batch_apply")
	}
	var done BatchResult
	if e := json.Unmarshal(stored, &done); e != nil {
		return res, e
	}
	for i := len(done.Steps) - 1; i >= 0; i-- {
		st := done.Steps[i]
		var rec struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(st.Result, &rec)
		inv, ok := inverse[st.Operation]
		if !ok {
			res.Steps = append(res.Steps, UndoStep{st.Operation, rec.ID, "not_undone"})
			continue
		}
		var current int64
		var removed bool
		if e := u.tx.QueryRow(ctx, fmt.Sprintf("SELECT version, %s IS NOT NULL FROM life.%s WHERE id=$1 FOR UPDATE", inv.removed, inv.table), rec.ID).Scan(&current, &removed); e != nil {
			return res, failedAt(i, st.Operation, e)
		}
		if removed {
			res.Steps = append(res.Steps, UndoStep{st.Operation, rec.ID, "already_removed"})
			continue
		}
		raw, _ := json.Marshal(map[string]any{"id": rec.ID, "expected_version": current, "idempotency_key": fmt.Sprintf("%s:%d", in.IdempotencyKey, i)})
		op := s.operations[inv.op]
		input, _, e := op.decode(raw)
		if e == nil {
			_, e = op.run(ctx, u, input)
		}
		if e != nil {
			return res, failedAt(i, inv.op, e)
		}
		res.Steps = append(res.Steps, UndoStep{st.Operation, rec.ID, "removed"})
	}
	return res, nil
}
