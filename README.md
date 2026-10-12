# Life Engine

The shared personal data layer for the agent harness: the system of record for what an agent keeps for a person (reminders now; goals, plan, routines, journal and notes next) plus the deterministic rules over them. One operation registry serves the router (`POST /api/v1/operations/<name>`, for apps) and MCP (`/mcp`, for the agent via mcp-hub). Design, contract and plan live in Project Hub: [`projects/life-engine/`](https://github.com/kumarabd/project-hub/tree/main/projects/life-engine).

## Status

Work packages E0 (bootstrap) and E1 (core: registry, idempotency, versions, audit, event contract) are in place, with reminders (including repeating ones) and `events_pending`/`events_ack` as the first domain slice. Not yet built: schedules, goals, plan, routines, journal, notes, captures, attachments (see the plan).

## Run

```sh
make test-db                       # disposable Postgres on :56393
export TEST_DATABASE_URL=postgres://life_test:local-life-test-only@127.0.0.1:56393/life_test
make check                         # format, vet, unit tests, build, OpenAPI drift
make integration                   # full tests against Postgres, with -race
DATABASE_URL=$TEST_DATABASE_URL make run   # serves on 127.0.0.1:8093
```

`make contract` regenerates `api/openapi.json` after changing operations.

## Conventions (same as finance-engine and retro)

- Writes carry `idempotency_key`; a retry replays the stored response. Edits carry `expected_version`; a stale one returns `conflict`.
- Errors are `{"error":{"code","message"}}`: `invalid_input` 400, `not_found` 404, `conflict`/`duplicate` 409, `too_large` 413, `busy` 503, `internal` 500.
- One person per database; the engine has no authentication or user handling. The router verifies Clerk and the tenant's network policy is the boundary, so never expose this service directly.
- Timestamps are RFC 3339 UTC. Recurring reminders repeat on their own IANA zone's wall-clock time.
- No timers and no outbound calls: due notices are derived when `events_pending` is polled by mcp-hub, which routes them to the user or the agent.
- Add a numbered migration; never edit an applied one.
