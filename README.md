# Life Engine

The shared personal data layer for the agent harness: the system of record for a person's **goals, tasks and events**, and the alerts on them. A reminder is an alert on a task or event, a routine is a task that repeats, and the day is a view, not a record ([ADR 0008](https://github.com/kumarabd/project-hub/blob/main/projects/life-engine/adr/0008-goal-task-event-primitives.md)). One operation registry serves the router (`POST /api/v1/operations/<name>`, for apps) and MCP (`/mcp`, for the agent via mcp-hub). Design, contract and plan live in Project Hub: [`projects/life-engine/`](https://github.com/kumarabd/project-hub/tree/main/projects/life-engine).

## Status

Clean-slate rebuild on branch `clean-slate-engine` (2026-10-21), not yet merged or deployed. Implemented and tested against Postgres 17 and a real Temporal server: goals (progress derived from linked tasks), tasks and calendar events (versioned, soft-delete, repeating on the person's local clock, alert before, snooze, per-occurrence completion), a `subject` of person or agent on every item, `day_get`, `batch_apply`/`batch_undo`/`batch_dry_run`, settings, audit history, and the `events_pending`/`events_ack` contract (notices only).

The engine owns the clock: it keeps a Temporal Schedule matched to each item with an alert (`internal/clock`, updated in place, native recurrence in the item's zone) and runs the worker for `FireWorkflow`. A person's alert becomes a notice the hub delivers; an agent's alert signals the conversation it was created from. Needs `TEMPORAL_ADDRESS` and `TENANT_SLUG` together (alerts are off without them). Migration 0022 drops the older model (reminders, schedules, routines, plan, steps, commitments, monitors, trackers and views, journal, notes, captures, attachments): nothing is migrated.

Not here: views and trackers (deferred, to be rebuilt on this base), and the client apps, which still speak the old operations.

## Run

```sh
make test-db                       # disposable Postgres on :56393
export TEST_DATABASE_URL=postgres://life_test:local-life-test-only@127.0.0.1:56393/life_test
make check                         # format, vet, unit tests, build, OpenAPI drift
make integration                   # full tests against Postgres, with -race
# LIVE_TEMPORAL_ADDRESS=host:7233 go test ./internal/clock -run Live   # real Temporal; needs TenantSlug, WakeKind, WakeSubject, WakeUserVisible registered
DATABASE_URL=$TEST_DATABASE_URL make run   # serves on 127.0.0.1:8093
```

`make contract` regenerates `api/openapi.json` after changing operations.

## Conventions (same as finance-engine and retro)

- Writes carry `idempotency_key`; a retry replays the stored response. Edits carry `expected_version`; a stale one returns `conflict`.
- Errors are `{"error":{"code","message"}}`: `invalid_input` 400, `not_found` 404, `conflict`/`duplicate` 409, `too_large` 413, `busy` 503, `internal` 500.
- One person per database; the engine has no authentication or user handling. The router verifies Clerk and the tenant's network policy is the boundary, so never expose this service directly.
- Timestamps are RFC 3339 UTC. A repeating item repeats on its own IANA zone's wall-clock time.
- The engine owns the clock (Temporal Schedules, see Status). A person's alert is recorded as a notice that mcp-hub collects with `events_pending`; an agent's alert signals the conversation it was created from.
- Add a numbered migration; never edit an applied one.
- "Today" is computed in the time zone from `settings_get`; set it first.

## Client fixtures

`fixtures/*.json` are real responses recorded from this engine (regenerate with
`LIFE_WRITE_FIXTURES=$PWD/fixtures go test ./internal/engine -run TestWriteClientFixtures`, needs `TEST_DATABASE_URL`). Client apps
decode them in their own tests so a contract change shows up as a failing test, not a broken screen; the iOS app keeps a copy
under `Tests/Fixtures/life`. IDs the scenario chooses are fixed; timestamps are whatever the server produced.
