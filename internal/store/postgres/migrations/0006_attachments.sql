-- Metadata for files attached to journal entries and notes; the bytes live in object storage.
CREATE TABLE life.attachments (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_type   text NOT NULL CHECK (owner_type IN ('journal_entry','note')),
  owner_id     uuid NOT NULL,
  filename     text NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 200),
  content_type text NOT NULL,
  size_bytes   bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 26214400),
  sha256       text,
  state        text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','ready')),
  version      bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_owner_idx ON life.attachments (owner_type, owner_id);
