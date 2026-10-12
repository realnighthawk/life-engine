-- One row of personal settings. "Today" for every day-based operation is computed in this zone.
CREATE TABLE life.settings (
  id         uuid PRIMARY KEY,
  time_zone  text NOT NULL DEFAULT 'UTC',
  work_start text NOT NULL DEFAULT '09:00' CHECK (work_start ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  work_end   text NOT NULL DEFAULT '17:00' CHECK (work_end ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  version    bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO life.settings (id) VALUES ('00000000-0000-0000-0000-000000000001');

-- Recurring prompts the person asked the agent to run ("every Sunday, review my week"). Visible and editable by the person.
CREATE TABLE life.schedules (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
  instruction   text NOT NULL CHECK (char_length(instruction) BETWEEN 1 AND 2000),
  time_of_day   text NOT NULL CHECK (time_of_day ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  time_zone     text NOT NULL,
  recurrence    jsonb NOT NULL,
  enabled       boolean NOT NULL DEFAULT true,
  next_at       timestamptz NOT NULL,
  last_fired_at timestamptz,
  version       bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX schedules_name_idx ON life.schedules (lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX schedules_next_idx ON life.schedules (next_at) WHERE deleted_at IS NULL AND enabled;
