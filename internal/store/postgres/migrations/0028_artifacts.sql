CREATE TABLE life.artifacts (
    id uuid PRIMARY KEY,
    title text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('document', 'deck')),
    catalog_id text NOT NULL,
    pages jsonb NOT NULL,
    version bigint NOT NULL DEFAULT 1
);
