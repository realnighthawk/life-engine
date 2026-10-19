-- A view is now pages of titled sections of blocks, not one flat list. A view in the flat shape cannot be read as the
-- new one, so it is retired; the goal shows no view until the assistant composes a new one.
UPDATE life.views SET deleted_at = now(), version = version + 1, updated_at = now() WHERE deleted_at IS NULL;
ALTER TABLE life.views ADD COLUMN pages jsonb NOT NULL DEFAULT '[]';
ALTER TABLE life.views DROP COLUMN blocks;
ALTER TABLE life.views ALTER COLUMN pages DROP DEFAULT;
