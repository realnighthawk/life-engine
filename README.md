# Life Engine

The shared personal data layer for the agent harness: the system of record for what an agent keeps for a person (reminders now; goals, plan, routines, journal and notes next) plus the deterministic rules over them. One operation registry serves the router (`POST /api/v1/operations/<name>`, for apps) and MCP (`/mcp`, for the agent via mcp-hub). Design, contract and plan live in Project Hub: [`projects/life-engine/`](https://github.com/kumarabd/project-hub/tree/main/projects/life-engine).

## Status

Implemented and tested: settings; reminders (repeating, on the person's local clock); schedules (recurring prompts for the agent); goals, steps and weekly commitments; the daily plan with agent-proposed revisions; routines with busy-day versions, check-ins, streaks and gap detection; journal and notes with full-text search; captures; attachments in object storage; `next_action`, `plan_replan_preview`, `day_close`, `week_stats`, `life_context_get`; audit history; and the `events_pending`/`events_ack` contract (reminders, schedules, routine nudges and gaps, captures). 77 operations in `api/openapi.json`.

Not yet: router/chart wiring and the mcp-hub catalog entry (they live in `agent-harness` and `infra/mcp-hub`), shared client fixtures, and gateway push.

## Run

```sh
make test-db                       # disposable Postgres on :56393
make test-minio                    # optional: MinIO on :59003, for the object-storage tests
export TEST_DATABASE_URL=postgres://life_test:local-life-test-only@127.0.0.1:56393/life_test
make check                         # format, vet, unit tests, build, OpenAPI drift
make integration                   # full tests against Postgres (and MinIO if MINIO_TEST_* is set), with -race
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
- Attachments are optional: set `MINIO_*` (see `.env.example`) to enable them. Bytes go through the engine (`PUT`/`GET /api/v1/attachments/<id>/content`), since object storage is not reachable from clients. Objects are immutable and checksummed.
- "Today" and the week are computed in the time zone from `settings_get`; set it first.
