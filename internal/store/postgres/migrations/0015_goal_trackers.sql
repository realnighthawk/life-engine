ALTER TABLE life.goals ADD COLUMN tracker jsonb;
ALTER TABLE life.goals ADD COLUMN tracker_revision bigint NOT NULL DEFAULT 0 CHECK (tracker_revision >= 0);

CREATE TABLE life.tracker_proposals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  goal_id uuid NOT NULL REFERENCES life.goals(id),
  base_goal_version bigint NOT NULL,
  base_tracker_revision bigint NOT NULL,
  definition jsonb NOT NULL,
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1000),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','applied','discarded')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tracker_proposals_goal_idx ON life.tracker_proposals(goal_id, state, created_at DESC);

CREATE TABLE life.goal_measurements (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  goal_id uuid NOT NULL REFERENCES life.goals(id),
  metric_id text NOT NULL CHECK (char_length(metric_id) BETWEEN 1 AND 64),
  unit text NOT NULL CHECK (char_length(unit) BETWEEN 1 AND 24),
  value double precision NOT NULL CHECK (value > '-Infinity'::float8 AND value < 'Infinity'::float8),
  measured_at timestamptz NOT NULL DEFAULT now(),
  notes text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 1000),
  version bigint NOT NULL DEFAULT 1,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX goal_measurements_series_idx ON life.goal_measurements(goal_id, metric_id, measured_at DESC, id) WHERE deleted_at IS NULL;
