package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Operation struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Write        bool               `json:"write"`
	InputSchema  *jsonschema.Schema `json:"input_schema"`
	OutputSchema *jsonschema.Schema `json:"output_schema"`
	keyless      bool
	decode       func([]byte) (any, []byte, error)
	run          func(context.Context, *unit, any) (any, error)
}
type Service struct {
	pool       *pgxpool.Pool
	operations map[string]*Operation
	waker      Waker
	nudge      chan struct{}
}

// unit is one operation's transaction. Every mutation and its audit row commit together.
type unit struct {
	tx               pgx.Tx
	actor, operation string
	session          string // the agent session this call came from, if any
}

// New builds the service.
func New(pool *pgxpool.Pool) *Service {
	s := &Service{pool: pool, operations: map[string]*Operation{}}
	s.registerOperations()
	return s
}

// register adds an operation. Writes must carry an idempotency_key (embed Meta in the input).
func register[I, O any](s *Service, name, description string, write bool, run func(context.Context, *unit, I) (O, error)) {
	add(s, name, description, write, false, run)
}

// registerKeyless adds a write the hub's delivery loop calls blind, with no idempotency key; it must be idempotent by construction.
func registerKeyless[I, O any](s *Service, name, description string, run func(context.Context, *unit, I) (O, error)) {
	add(s, name, description, true, true, run)
}

func add[I, O any](s *Service, name, description string, write, keyless bool, run func(context.Context, *unit, I) (O, error)) {
	input, err := jsonschema.For[I](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): {},
		reflect.TypeFor[Calc]():            calcSchema(),
	}})
	if err != nil {
		panic(err)
	}
	output, err := jsonschema.For[O](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): {Types: []string{"object", "null"}},
		reflect.TypeFor[Calc]():            calcSchema(),
	}})
	if err != nil {
		panic(err)
	}
	resolved, err := input.Resolve(nil)
	if err != nil {
		panic(err)
	}
	s.operations[name] = &Operation{
		Name: name, Description: description, Write: write, keyless: keyless, InputSchema: input, OutputSchema: output,
		decode: func(raw []byte) (any, []byte, error) {
			if len(raw) == 0 {
				raw = []byte("{}")
			}
			var shape map[string]any
			if err := json.Unmarshal(raw, &shape); err != nil || shape == nil {
				return nil, nil, invalid("Input must be a JSON object")
			}
			if err := resolved.Validate(shape); err != nil {
				return nil, nil, invalid("Input does not match the operation schema: " + err.Error())
			}
			var input I
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return nil, nil, invalid("Invalid input: " + err.Error())
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return nil, nil, invalid("Expected one JSON object")
			}
			canonical, err := json.Marshal(input)
			if err == nil {
				decoder := json.NewDecoder(bytes.NewReader(canonical))
				decoder.UseNumber()
				var normalized any
				if err = decoder.Decode(&normalized); err == nil {
					canonical, err = json.Marshal(normalized)
				}
			}
			return input, canonical, err
		},
		run: func(ctx context.Context, u *unit, arg any) (any, error) { return run(ctx, u, arg.(I)) },
	}
}

func (s *Service) Operations() []*Operation {
	ops := make([]*Operation, 0, len(s.operations))
	for _, op := range s.operations {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return ops
}

func (s *Service) Execute(ctx context.Context, name string, raw []byte) (json.RawMessage, error) {
	op, ok := s.operations[name]
	if !ok {
		return nil, &Error{"not_found", "Unknown operation"}
	}
	input, canonical, err := op.decode(raw)
	if err != nil {
		return nil, err
	}
	access := pgx.ReadOnly
	isolation := pgx.RepeatableRead
	key := ""
	keyed := op.Write && !op.keyless
	if op.Write {
		access = pgx.ReadWrite
		isolation = pgx.ReadCommitted
	}
	if keyed {
		var meta Meta
		if err := json.Unmarshal(canonical, &meta); err != nil {
			return nil, err
		}
		key = meta.IdempotencyKey
		if len(key) < 1 || len(key) > 128 || strings.TrimSpace(key) != key {
			return nil, invalid("idempotency_key must contain 1-128 characters without surrounding spaces")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation, AccessMode: access})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	u := &unit{tx: tx, actor: "router", operation: name, session: sessionFrom(ctx)}
	sum := sha256.Sum256(canonical)
	hash := hex.EncodeToString(sum[:])
	if op.Write {
		// One person's data per database: serialize mutations across replicas. After the lock, READ COMMITTED sees
		// preceding writes; version checks still reject stale edits.
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(91438)"); err != nil {
			return nil, err
		}
	}
	if keyed {
		var oldOp, oldHash string
		var response []byte
		err = tx.QueryRow(ctx, "SELECT operation,input_hash,response FROM life.requests WHERE key=$1", key).Scan(&oldOp, &oldHash, &response)
		if err == nil {
			if oldOp != name || oldHash != hash {
				return nil, conflict("Idempotency key was already used for different input")
			}
			return response, nil
		}
		if err != pgx.ErrNoRows {
			return nil, err
		}
	}
	result, err := op.run(ctx, u, input)
	if err != nil {
		return nil, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if keyed {
		if _, err = tx.Exec(ctx, "INSERT INTO life.requests(key,operation,input_hash,response) VALUES ($1,$2,$3,$4)", key, name, hash, response); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if op.Write && s.nudge != nil {
		select {
		case s.nudge <- struct{}{}:
		default:
		}
	}
	return response, nil
}

func (u *unit) audit(ctx context.Context, kind, id string, before, after any) error {
	var b, a []byte
	var err error
	if before != nil {
		if b, err = json.Marshal(before); err != nil {
			return err
		}
	}
	if after != nil {
		if a, err = json.Marshal(after); err != nil {
			return err
		}
	}
	_, err = u.tx.Exec(ctx, `INSERT INTO life.changes(actor_id,operation,entity_type,entity_id,before_data,after_data)
        VALUES($1,$2,$3,$4,$5,$6)`, u.actor, u.operation, kind, id, b, a)
	return err
}

func (u *unit) now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := u.tx.QueryRow(ctx, "SELECT now()").Scan(&t)
	return t, err
}

func version(expected, current int64) error {
	if expected < 1 {
		return invalid("expected_version must be positive")
	}
	if expected != current {
		return conflict(fmt.Sprintf("Stale version: expected %d, current %d. Fetch the record and reapply your edit.", expected, current))
	}
	return nil
}
