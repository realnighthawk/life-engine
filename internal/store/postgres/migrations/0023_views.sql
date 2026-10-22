-- A view is its own construct (project-hub life-engine ADR 0008, amended): a manifest of blocks, composed from the view
-- kit, that refers to a subject (a goal, a task or an event) and to metric definitions, never inlining the records it shows.
-- Each client draws the same manifest with its own components. One live view per subject and client catalog. Observations
-- a person logs by hand for a view's metrics are kept per subject.
CREATE TABLE life.views (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_kind text NOT NULL CHECK (subject_kind IN ('goal', 'task', 'event')),
  subject_id   uuid NOT NULL,
  catalog_id   text NOT NULL CHECK (char_length(catalog_id) BETWEEN 1 AND 128),
  metrics      jsonb NOT NULL DEFAULT '[]',
  pages        jsonb NOT NULL,
  version      bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX views_subject_catalog_idx ON life.views(subject_kind, subject_id, catalog_id) WHERE deleted_at IS NULL;

CREATE TABLE life.measurements (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_kind text NOT NULL CHECK (subject_kind IN ('goal', 'task', 'event')),
  subject_id   uuid NOT NULL,
  metric_id    text NOT NULL CHECK (char_length(metric_id) BETWEEN 1 AND 64),
  unit         text NOT NULL CHECK (char_length(unit) BETWEEN 1 AND 24),
  value        double precision NOT NULL CHECK (value > '-Infinity'::float8 AND value < 'Infinity'::float8),
  measured_at  timestamptz NOT NULL DEFAULT now(),
  notes        text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 1000),
  version      bigint NOT NULL DEFAULT 1,
  deleted_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX measurements_series_idx ON life.measurements(subject_kind, subject_id, metric_id, measured_at DESC, id) WHERE deleted_at IS NULL;
