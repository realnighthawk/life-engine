-- project-hub ADR 0010 cutover. The old model is deleted, not migrated: goals, items and occurrences go, and tasks
-- and events become separate records with their own lifecycles. No compatibility layer.

ALTER TABLE life.watches DROP COLUMN IF EXISTS goal_id;
ALTER TABLE life.watches ADD COLUMN intention_id uuid REFERENCES life.intentions(id);

DROP TABLE IF EXISTS life.occurrences, life.items, life.goals CASCADE;

ALTER TABLE life.views DROP CONSTRAINT IF EXISTS views_subject_kind_check;
UPDATE life.views SET subject_kind='intention' WHERE subject_kind='goal';
ALTER TABLE life.views ADD CONSTRAINT views_subject_kind_check CHECK (subject_kind IN ('intention', 'task', 'event'));

ALTER TABLE life.measurements DROP CONSTRAINT IF EXISTS measurements_subject_kind_check;
UPDATE life.measurements SET subject_kind='intention' WHERE subject_kind='goal';
ALTER TABLE life.measurements ADD CONSTRAINT measurements_subject_kind_check CHECK (subject_kind IN ('intention', 'task', 'event'));

-- A task waits for someone to do it. A time on it is a deadline or a cue; repeat makes it a routine, and an alert makes it
-- a reminder. One-off tasks finish as a whole; a repeating one finishes one occurrence at a time, each recorded as an
-- activity linked to the task.
CREATE TABLE life.tasks (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject              text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  title                text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  notes                text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  at                   timestamptz,
  minutes              integer CHECK (minutes IS NULL OR minutes BETWEEN 1 AND 1440),
  time_zone            text NOT NULL DEFAULT 'UTC',
  recurrence           jsonb,
  alert_before_minutes integer CHECK (alert_before_minutes IS NULL OR alert_before_minutes BETWEEN 0 AND 10080),
  is_priority          boolean NOT NULL DEFAULT false,
  session_key          text,
  snoozed_until        timestamptz,
  armed                text,
  done_at              timestamptz,
  result               text NOT NULL DEFAULT '' CHECK (char_length(result) <= 2000),
  origin               text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from         jsonb,
  version              bigint NOT NULL DEFAULT 1,
  deleted_at           timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (recurrence IS NULL OR at IS NOT NULL),
  CHECK (alert_before_minutes IS NULL OR at IS NOT NULL)
);
CREATE INDEX tasks_at_idx ON life.tasks (at) WHERE deleted_at IS NULL;
CREATE INDEX tasks_session_idx ON life.tasks (session_key) WHERE session_key IS NOT NULL;

-- An event happens at its time whether or not anyone acts. It is not completable; it is cancelled or it occurs.
CREATE TABLE life.events (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject              text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  title                text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  notes                text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  at                   timestamptz NOT NULL,
  minutes              integer CHECK (minutes IS NULL OR minutes BETWEEN 1 AND 1440),
  time_zone            text NOT NULL DEFAULT 'UTC',
  recurrence           jsonb,
  alert_before_minutes integer CHECK (alert_before_minutes IS NULL OR alert_before_minutes BETWEEN 0 AND 10080),
  session_key          text,
  snoozed_until        timestamptz,
  armed                text,
  cancelled_at         timestamptz,
  origin               text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from         jsonb,
  version              bigint NOT NULL DEFAULT 1,
  deleted_at           timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (recurrence IS NULL OR at IS NOT NULL),
  CHECK (alert_before_minutes IS NULL OR at IS NOT NULL)
);
CREATE INDEX events_at_idx ON life.events (at) WHERE deleted_at IS NULL;
CREATE INDEX events_session_idx ON life.events (session_key) WHERE session_key IS NOT NULL;
