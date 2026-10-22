-- project-hub ADR 0008: the engine holds a person's goals, tasks and events, and nothing else. A reminder is an alert on one,
-- a routine is a task that repeats, a step is a task linked to a goal, and the day is a view. Everything the older model
-- stored is dropped, not migrated (owner's clean slate): reminders, schedules, routines and nudges, plan items and
-- proposals, commitments, monitors, steps, goal measurements and views, journal, notes, captures and attachments.
-- Kept: the audit trail, idempotency keys, notices and their acknowledgements, and settings.

DROP TABLE IF EXISTS life.attachments, life.captures, life.commitments, life.goal_measurements, life.goal_routines,
  life.journal_entries, life.monitors, life.notes, life.plan_items, life.plan_proposals, life.reminder_acks,
  life.reminders, life.routine_checkins, life.routine_nudges, life.routines, life.schedules, life.steps, life.views,
  life.goals CASCADE;
DELETE FROM life.changes;
DELETE FROM life.acked_events;
DELETE FROM life.notices;
DELETE FROM life.requests;

-- A goal is an outcome. Finite ("Lisbon, June 3-10") when it has an end date, open-ended ("get fit") when it has none.
-- Its progress is derived from the tasks linked to it, never stored.
CREATE TABLE life.goals (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject    text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  notes      text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  ends_on    date,
  version    bigint NOT NULL DEFAULT 1,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- A task waits for someone to do it; an event happens at its time. Either may repeat and either may alert. `at` is the
-- time (a task's deadline or cue, an event's start) and anchors a repeating series. Whose it is is `subject`; an agent's
-- item delivers into `session_key` when its alert fires, a person's becomes a notice.
CREATE TABLE life.items (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind                 text NOT NULL CHECK (kind IN ('task', 'event')),
  subject              text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  title                text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  notes                text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  goal_id              uuid REFERENCES life.goals(id),
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
  version              bigint NOT NULL DEFAULT 1,
  deleted_at           timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (kind = 'task' OR done_at IS NULL),
  CHECK (recurrence IS NULL OR at IS NOT NULL),
  CHECK (alert_before_minutes IS NULL OR at IS NOT NULL)
);
CREATE INDEX items_goal_idx ON life.items (goal_id) WHERE goal_id IS NOT NULL;
CREATE INDEX items_at_idx ON life.items (at) WHERE deleted_at IS NULL;

-- One row per occurrence that has been dealt with. A one-off task is finished by done_at on the item; a repeating one is
-- finished one occurrence at a time, here. `result` is what an agent's own item found or did when it ran.
CREATE TABLE life.occurrences (
  item_id uuid NOT NULL REFERENCES life.items(id) ON DELETE CASCADE,
  at      timestamptz NOT NULL,
  state   text NOT NULL CHECK (state IN ('done', 'acknowledged')),
  result  text NOT NULL DEFAULT '' CHECK (char_length(result) <= 2000),
  acted_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (item_id, at)
);
