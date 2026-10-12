-- Idempotency: a retried write with the same key replays the stored response.
CREATE TABLE life.requests (
  key        text PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 128),
  operation  text NOT NULL,
  input_hash text NOT NULL,
  response   jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only audit of every record change.
CREATE TABLE life.changes (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_id    text NOT NULL,
  operation   text NOT NULL,
  entity_type text NOT NULL,
  entity_id   text NOT NULL,
  before_data jsonb,
  after_data  jsonb,
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX changes_entity_idx ON life.changes (entity_type, entity_id, id DESC);

-- Notices already handed to the delivery loop. Pending notices are derived from records, so this is the only delivery state.
CREATE TABLE life.acked_events (
  event_id text PRIMARY KEY,
  acked_at timestamptz NOT NULL DEFAULT now()
);
