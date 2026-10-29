-- project-hub ADR 0010 / specs/personal-model.md: the records the engine did not have. This is the first half of the
-- cutover. The next migration drops `goals`, `items` and `occurrences` once the clock, day view, alerts, views and watches
-- read the new tables; both halves land on this branch before merge. No compatibility shim and no data migration.

-- An intention is a standing want: achieve, maintain or experience. A goal only expressed an outcome; an intention may
-- have a target, an end date, both or neither. `origin` says who authored it; `derived_from` names the memory candidate
-- and evidence snapshot it came from, when an agent promoted one.
CREATE TABLE life.intentions (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject      text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  title        text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  kind         text NOT NULL DEFAULT 'achieve' CHECK (kind IN ('achieve', 'maintain', 'experience')),
  notes        text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  ends_on      date,
  target       jsonb,
  status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'satisfied', 'abandoned')),
  origin       text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from jsonb,
  version      bigint NOT NULL DEFAULT 1,
  deleted_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX intentions_subject_idx ON life.intentions (subject) WHERE deleted_at IS NULL;
CREATE INDEX intentions_ends_idx ON life.intentions (ends_on) WHERE deleted_at IS NULL;

-- A commitment is an obligation: something owed to a person, an institution or oneself. It is not a task; it generates
-- the task that discharges it. `due_at` is an instant; `due_on` is a local date for an obligation that belongs to a day
-- (rent on the 1st) rather than a time.
CREATE TABLE life.commitments (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject              text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  title                text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  notes                text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  counterparty         text NOT NULL DEFAULT '' CHECK (char_length(counterparty) <= 200),
  consequence          text NOT NULL DEFAULT '' CHECK (char_length(consequence) <= 500),
  due_at               timestamptz,
  due_on               date,
  recurrence           jsonb,
  alert_before_minutes integer CHECK (alert_before_minutes IS NULL OR alert_before_minutes BETWEEN 0 AND 10080),
  time_zone            text NOT NULL DEFAULT 'UTC',
  status               text NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'met', 'missed', 'renegotiated', 'waived')),
  origin               text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from         jsonb,
  version              bigint NOT NULL DEFAULT 1,
  deleted_at           timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (due_at IS NULL OR due_on IS NULL),
  CHECK (recurrence IS NULL OR due_at IS NOT NULL),
  CHECK (alert_before_minutes IS NULL OR due_at IS NOT NULL)
);
CREATE INDEX commitments_due_idx ON life.commitments (status, due_at) WHERE deleted_at IS NULL;
CREATE INDEX commitments_day_idx ON life.commitments (status, due_on) WHERE deleted_at IS NULL;

-- A decision is a choice the person is facing or has made. `kind=change` makes it the promotion gate: `effect` is a
-- batch of ordinary create operations, applied all-or-nothing when the option named `accept` is chosen. A change
-- decision derived from memory also requires a person-sourced confirmation to resolve.
CREATE TABLE life.decisions (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject       text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  question      text NOT NULL CHECK (char_length(question) BETWEEN 1 AND 500),
  kind          text NOT NULL DEFAULT 'choice' CHECK (kind IN ('choice', 'change')),
  options       jsonb NOT NULL CHECK (jsonb_typeof(options) = 'array'),
  status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'decided', 'deferred', 'expired')),
  chosen_option text,
  rationale     text NOT NULL DEFAULT '' CHECK (char_length(rationale) <= 2000),
  decided_by    text CHECK (decided_by IN ('person', 'agent')),
  decided_at    timestamptz,
  expires_at    timestamptz,
  effect        jsonb,
  origin        text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from  jsonb,
  version       bigint NOT NULL DEFAULT 1,
  deleted_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (kind <> 'change' OR effect IS NOT NULL)
);
CREATE INDEX decisions_open_idx ON life.decisions (status, expires_at) WHERE deleted_at IS NULL;

-- A fact the person has confirmed about themselves, their environment or their resources. `states` rather than `facts`
-- because agent-brain already owns extracted facts; these are asserted, not derived.
CREATE TABLE life.states (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject      text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  kind         text NOT NULL DEFAULT 'fact' CHECK (kind IN ('fact', 'preference', 'constraint', 'need')),
  topic        text NOT NULL CHECK (char_length(topic) BETWEEN 1 AND 200),
  predicate    text NOT NULL CHECK (char_length(predicate) BETWEEN 1 AND 200),
  value        jsonb,
  notes        text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
  valid_from   timestamptz,
  valid_to     timestamptz,
  source       text NOT NULL DEFAULT 'person' CHECK (source IN ('person', 'agent', 'observation')),
  status       text NOT NULL DEFAULT 'confirmed' CHECK (status IN ('confirmed', 'retracted', 'superseded')),
  origin       text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from jsonb,
  version      bigint NOT NULL DEFAULT 1,
  deleted_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to >= valid_from)
);
CREATE INDEX states_topic_idx ON life.states (subject, topic) WHERE deleted_at IS NULL;

-- What happened. Append-only: a correction changes the text and is audited, never silently rewrites history.
CREATE TABLE life.activities (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject      text NOT NULL DEFAULT 'person' CHECK (subject IN ('person', 'agent')),
  actor        text NOT NULL CHECK (actor IN ('person', 'agent', 'external')),
  kind         text NOT NULL DEFAULT 'action' CHECK (kind IN ('action', 'occurrence', 'measurement')),
  text         text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 2000),
  occurred_at  timestamptz NOT NULL,
  source       text NOT NULL DEFAULT 'person' CHECK (source IN ('person', 'agent', 'connector')),
  origin       text NOT NULL DEFAULT 'person' CHECK (origin IN ('person', 'agent')),
  derived_from jsonb,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX activities_time_idx ON life.activities (occurred_at DESC);
CREATE INDEX activities_subject_idx ON life.activities (subject, occurred_at DESC);

-- One edge table instead of a nullable foreign key per pair. Kinds name the seven records; `goal` and `item` are the
-- pre-ADR-0010 tables, kept while they still exist. Integrity is the engine's, not a foreign key: the target may live in
-- any of several tables.
CREATE TABLE life.links (
  from_kind  text NOT NULL CHECK (from_kind IN ('intention', 'commitment', 'decision', 'state', 'activity', 'task', 'event', 'goal', 'item')),
  from_id    uuid NOT NULL,
  relation   text NOT NULL CHECK (relation IN ('serves', 'fulfils', 'affects', 'constrains', 'evidences')),
  to_kind    text NOT NULL CHECK (to_kind IN ('intention', 'commitment', 'decision', 'state', 'activity', 'task', 'event', 'goal', 'item')),
  to_id      uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (from_kind, from_id, relation, to_kind, to_id),
  CHECK (from_kind <> to_kind OR from_id <> to_id)
);
CREATE INDEX links_to_idx ON life.links (to_kind, to_id, relation);
CREATE INDEX links_from_idx ON life.links (from_kind, from_id, relation);
