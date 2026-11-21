-- An artifact can be removed from the Library without erasing its audit history.
ALTER TABLE life.artifacts ADD COLUMN deleted_at timestamptz;
CREATE INDEX artifacts_live_updated_idx ON life.artifacts (updated_at DESC, id DESC) WHERE deleted_at IS NULL;
