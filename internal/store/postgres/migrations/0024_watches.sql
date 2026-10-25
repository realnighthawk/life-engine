-- A watch is the agent's standing instruction to be woken when an event happens (project-hub ADR 0009). The engine owns the
-- record, like its other agent items; mcp-hub only carries events to it. The hub used to hold these as subscriptions of
-- its own, which left the agent's monitors in two places.
--
-- Identity is (session, name): arming the same name again revises the watch instead of adding a second. session_key is the
-- user-stable conversation the wake goes to, not the branch it was armed from.
CREATE TABLE life.watches (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,59}$'),
  session_key text NOT NULL CHECK (char_length(session_key) BETWEEN 1 AND 500),
  event_key   text NOT NULL CHECK (char_length(event_key) BETWEEN 1 AND 200),
  objective   text NOT NULL CHECK (char_length(objective) BETWEEN 1 AND 2000),
  goal_id     uuid REFERENCES life.goals(id),
  expires_at  timestamptz,
  version     bigint NOT NULL DEFAULT 1,
  deleted_at  timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX watches_name_uidx ON life.watches (session_key, name) WHERE deleted_at IS NULL;
CREATE INDEX watches_event_idx ON life.watches (event_key) WHERE deleted_at IS NULL;

-- One row per (watch, event occurrence): the dedupe, and the outbox the wake is sent from. The hub hands an event over once
-- this row exists; started_at is set when the wake workflow has been started, so a crash in between is retried, not lost.
CREATE TABLE life.watch_firings (
  watch_id   uuid NOT NULL REFERENCES life.watches(id),
  event_id   text NOT NULL CHECK (char_length(event_id) BETWEEN 1 AND 200),
  detail     text NOT NULL DEFAULT '',
  source     text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  PRIMARY KEY (watch_id, event_id)
);
CREATE INDEX watch_firings_pending_idx ON life.watch_firings (created_at) WHERE started_at IS NULL;
