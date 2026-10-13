-- A routine can be done several times a day (drink water 3 times) and nudged at several times; a day is done when its
-- count reaches the target. remind_at becomes the first cue. See project-hub ADR 0004.
ALTER TABLE life.routines
  ADD COLUMN target_per_day smallint NOT NULL DEFAULT 1 CHECK (target_per_day BETWEEN 1 AND 12),
  ADD COLUMN cues text[] NOT NULL DEFAULT '{}' CHECK (cardinality(cues) <= 8);
UPDATE life.routines SET cues = ARRAY[remind_at] WHERE remind_at IS NOT NULL;
ALTER TABLE life.routines DROP COLUMN remind_at;
ALTER TABLE life.routine_checkins ADD COLUMN count smallint NOT NULL DEFAULT 1 CHECK (count BETWEEN 1 AND 50);
