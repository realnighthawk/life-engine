CREATE TABLE life.reminders (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title           text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  notes           text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  due_at          timestamptz,
  snoozed_until   timestamptz,
  time_zone       text NOT NULL DEFAULT 'UTC',
  recurrence      jsonb,
  source          text NOT NULL DEFAULT 'user' CHECK (source IN ('user','agent')),
  session_id      text,
  turn_seq        bigint,
  link            jsonb,
  state           text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','done')),
  completed_at    timestamptz,
  last_completed_at timestamptz,
  version         bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at      timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (recurrence IS NULL OR due_at IS NOT NULL)
);
CREATE INDEX reminders_due_idx ON life.reminders (due_at) WHERE deleted_at IS NULL AND state = 'pending';
