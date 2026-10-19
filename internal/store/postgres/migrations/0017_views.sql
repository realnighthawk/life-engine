-- A view is its own object: a manifest of blocks that refer to a subject (today a goal) and to metric definitions, never
-- inlining the records it shows. One live view per subject and client catalog.
CREATE TABLE life.views (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_kind text NOT NULL CHECK (subject_kind IN ('goal')),
  subject_id uuid NOT NULL,
  catalog_id text NOT NULL CHECK (char_length(catalog_id) BETWEEN 1 AND 128),
  metrics jsonb NOT NULL DEFAULT '[]',
  blocks jsonb NOT NULL,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX views_subject_catalog_idx ON life.views(subject_kind, subject_id, catalog_id) WHERE deleted_at IS NULL;
