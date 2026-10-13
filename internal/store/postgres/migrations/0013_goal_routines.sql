-- Which routines serve a goal. A plain many-to-many link: a routine exists without a goal, can serve several, and removing either
-- removes only the link. See project-hub ADR 0004.
CREATE TABLE life.goal_routines (
  goal_id    uuid NOT NULL REFERENCES life.goals(id),
  routine_id uuid NOT NULL REFERENCES life.routines(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (goal_id, routine_id)
);
CREATE INDEX goal_routines_routine_idx ON life.goal_routines (routine_id);
