-- A nudge is a time at which the agent considers prompting the person about a routine. It belongs to its routine and cannot
-- exist without one; it is not a reminder and never appears in the reminders agenda. Replaces routines.cues (project-hub ADR 0007).
CREATE TABLE life.routine_nudges (
  routine_id uuid NOT NULL REFERENCES life.routines(id) ON DELETE CASCADE,
  at         text NOT NULL CHECK (at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  message    text NOT NULL DEFAULT '' CHECK (char_length(message) <= 200),
  PRIMARY KEY (routine_id, at)
);
INSERT INTO life.routine_nudges(routine_id, at) SELECT id, unnest(cues) FROM life.routines;
ALTER TABLE life.routines DROP COLUMN cues;
