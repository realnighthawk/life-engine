-- A schedule can be about a record (a goal check-in is a schedule linked to a goal), and a monitor is a recurring check
-- the agent runs on an interval and reports on. Both fire as wakes (ADR 0003).
ALTER TABLE life.schedules ADD COLUMN link jsonb;

CREATE TABLE life.monitors (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name              text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
  objective         text NOT NULL CHECK (char_length(objective) BETWEEN 1 AND 2000),
  every_minutes     integer NOT NULL CHECK (every_minutes BETWEEN 15 AND 44640),
  enabled           boolean NOT NULL DEFAULT true,
  next_at           timestamptz NOT NULL,
  last_checked_at   timestamptz,
  last_result       text NOT NULL DEFAULT '' CHECK (char_length(last_result) <= 1000),
  last_triggered_at timestamptz,
  session_id        text,
  armed_for         timestamptz,
  armed_session     text,
  version           bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at        timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX monitors_name_idx ON life.monitors (lower(name)) WHERE deleted_at IS NULL;
