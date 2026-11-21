ALTER TABLE life.artifacts
    ADD COLUMN agent_id text,
    ADD COLUMN tags text[] NOT NULL DEFAULT '{}',
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- Recover revision times from the existing audit history, which contains no creator identity.
UPDATE life.artifacts AS a
SET updated_at = latest.occurred_at
FROM (
    SELECT DISTINCT ON (entity_id) entity_id, occurred_at
    FROM life.changes
    WHERE entity_type = 'artifact'
    ORDER BY entity_id, id DESC
) AS latest
WHERE latest.entity_id = a.id::text;

CREATE INDEX artifacts_updated_idx ON life.artifacts (updated_at DESC, id DESC);
CREATE INDEX artifacts_agent_updated_idx ON life.artifacts (agent_id, updated_at DESC, id DESC)
    WHERE agent_id IS NOT NULL;
