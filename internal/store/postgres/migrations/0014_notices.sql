-- Notices the agent (or any caller) asks the engine to deliver to the person. Delivery is the hub's and gateway's job: a notice is
-- handed over by events_pending and acknowledged like any other event. See project-hub ADR 0005.
CREATE TABLE life.notices (
  id         uuid PRIMARY KEY,
  title      text NOT NULL,
  body       text NOT NULL DEFAULT '',
  priority   text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal', 'high')),
  link_entity text,
  link_id    text,
  created_at timestamptz NOT NULL DEFAULT now()
);
