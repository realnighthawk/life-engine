-- A goal's tracker lived on the goal. It is a view now: copy each saved presentation into life.views (the metrics are
-- shared, so each view carries them), then drop the old columns. A catalog the goal already has a view for is skipped.
INSERT INTO life.views(subject_kind, subject_id, catalog_id, metrics, blocks)
SELECT 'goal', g.id, v->>'catalog_id', CASE WHEN jsonb_typeof(g.tracker->'metrics') = 'array' THEN g.tracker->'metrics' ELSE '[]'::jsonb END, v->'sections'
FROM life.goals g CROSS JOIN LATERAL jsonb_array_elements(g.tracker->'views') v
WHERE g.tracker IS NOT NULL
ON CONFLICT DO NOTHING;

ALTER TABLE life.goals DROP COLUMN tracker, DROP COLUMN tracker_revision;
