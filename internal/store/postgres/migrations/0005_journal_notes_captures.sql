CREATE TABLE life.journal_entries (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  entry_at   timestamptz NOT NULL DEFAULT now(),
  text       text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 20000),
  goal_ids   uuid[] NOT NULL DEFAULT '{}',
  tomorrow   text CHECK (tomorrow IS NULL OR char_length(tomorrow) <= 500),
  source     text NOT NULL DEFAULT 'user' CHECK (source IN ('user','agent','day_close')),
  session_id text,
  turn_seq   bigint,
  tsv        tsvector GENERATED ALWAYS AS (to_tsvector('english', text)) STORED,
  version    bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX journal_entry_at_idx ON life.journal_entries (entry_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX journal_tsv_idx ON life.journal_entries USING gin (tsv);

CREATE TABLE life.notes (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  body       text NOT NULL DEFAULT '' CHECK (char_length(body) <= 50000),
  tags       text[] NOT NULL DEFAULT '{}',
  goal_id    uuid REFERENCES life.goals(id) ON DELETE SET NULL,
  pinned     boolean NOT NULL DEFAULT false,
  tsv        tsvector GENERATED ALWAYS AS (to_tsvector('english', title || ' ' || body)) STORED,
  version    bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notes_tsv_idx ON life.notes USING gin (tsv);
CREATE INDEX notes_tags_idx ON life.notes USING gin (tags);

-- Raw one-liners captured instantly (offline-safe); the agent or the person turns each into a proper record.
CREATE TABLE life.captures (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  text        text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 2000),
  captured_at timestamptz NOT NULL DEFAULT now(),
  source      text NOT NULL DEFAULT 'user' CHECK (source IN ('user','voice','agent')),
  session_id  text,
  turn_seq    bigint,
  state       text NOT NULL DEFAULT 'open' CHECK (state IN ('open','resolved')),
  resolved_as text CHECK (resolved_as IN ('reminder','plan_item','journal_entry','note','goal','dismissed')),
  resolved_id uuid,
  resolved_at timestamptz,
  version     bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX captures_open_idx ON life.captures (captured_at) WHERE state = 'open';
