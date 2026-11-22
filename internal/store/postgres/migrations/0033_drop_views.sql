-- ADR 0011: views and the numeric observations bound to their metrics are removed. Artifacts are templates over sources.
-- No data is migrated; intentions, tasks, events and their links are untouched.
DROP TABLE IF EXISTS life.measurements;
DROP TABLE IF EXISTS life.views;
-- Their audit rows describe records that no longer exist.
DELETE FROM life.changes WHERE entity_type IN ('view', 'measurement');
