-- Subject-linked views join standalone artifacts in the Library: the creator and tags travel with the manifest so one
-- index can list both without reading every subject.
ALTER TABLE life.views ADD COLUMN agent_id text;
ALTER TABLE life.views ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
CREATE INDEX views_updated_idx ON life.views (updated_at DESC, id) WHERE deleted_at IS NULL;
