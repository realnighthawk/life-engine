CREATE TABLE life.goals (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title         text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  why           text NOT NULL DEFAULT '' CHECK (char_length(why) <= 1000),
  weekly_target smallint NOT NULL DEFAULT 3 CHECK (weekly_target BETWEEN 0 AND 21),
  archived_at   timestamptz,
  version       bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE life.steps (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  goal_id    uuid NOT NULL REFERENCES life.goals(id),
  title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  minutes    smallint NOT NULL DEFAULT 15 CHECK (minutes BETWEEN 1 AND 480),
  position   integer NOT NULL CHECK (position > 0),
  done_at    timestamptz,
  version    bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX steps_goal_idx ON life.steps (goal_id, position);

-- What the person chose to do toward goals in a given week (week_start is the local Monday).
CREATE TABLE life.commitments (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  week_start date NOT NULL,
  title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  goal_id    uuid REFERENCES life.goals(id),
  step_id    uuid REFERENCES life.steps(id) ON DELETE SET NULL,
  done_at    timestamptz,
  version    bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX commitments_week_idx ON life.commitments (week_start);

CREATE TABLE life.plan_items (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  day         date NOT NULL,
  title       text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  kind        text NOT NULL DEFAULT 'task' CHECK (kind IN ('task','event','prep','rest')),
  minutes     smallint NOT NULL DEFAULT 25 CHECK (minutes BETWEEN 1 AND 720),
  start_at    timestamptz,
  is_priority boolean NOT NULL DEFAULT false,
  goal_id     uuid REFERENCES life.goals(id) ON DELETE SET NULL,
  step_id     uuid REFERENCES life.steps(id) ON DELETE SET NULL,
  done_at     timestamptz,
  version     bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at  timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX plan_items_day_idx ON life.plan_items (day) WHERE deleted_at IS NULL;

-- A revised plan the agent offers and the person accepts or discards.
CREATE TABLE life.plan_proposals (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  day        date NOT NULL,
  reason     text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1000),
  changes    jsonb NOT NULL,
  state      text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','applied','discarded')),
  version    bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE life.routines (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title         text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  minutes       smallint NOT NULL CHECK (minutes BETWEEN 1 AND 240),
  short_title   text NOT NULL CHECK (char_length(short_title) BETWEEN 1 AND 200),
  short_minutes smallint NOT NULL CHECK (short_minutes BETWEEN 1 AND 240),
  weekdays      smallint[] NOT NULL DEFAULT '{}',
  remind_at     text CHECK (remind_at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  time_zone     text NOT NULL,
  version       bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE life.routine_checkins (
  routine_id uuid NOT NULL REFERENCES life.routines(id),
  day        date NOT NULL,
  variant    text NOT NULL CHECK (variant IN ('full','short')),
  at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (routine_id, day)
);
