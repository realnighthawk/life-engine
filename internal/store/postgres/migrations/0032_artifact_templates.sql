-- ADR 0011: an artifact is {template, params, sources}. Existing artifacts keep their pages as the document or deck
-- template's params; the view kit's mode and catalog id go.
ALTER TABLE life.artifacts
    ADD COLUMN template text NOT NULL DEFAULT 'document.v1',
    ADD COLUMN params jsonb,
    ADD COLUMN sources jsonb NOT NULL DEFAULT '[]';

UPDATE life.artifacts
SET template = CASE mode WHEN 'deck' THEN 'deck.v1' ELSE 'document.v1' END,
    params = jsonb_build_object('pages', pages);

ALTER TABLE life.artifacts
    ALTER COLUMN params SET NOT NULL,
    DROP COLUMN pages,
    DROP COLUMN mode,
    DROP COLUMN catalog_id;
