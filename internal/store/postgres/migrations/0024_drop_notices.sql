-- The engine no longer records notices: a person's alert is posted straight to the gateway when it fires, and the agent
-- reaches the person with notify_user (project-hub life-engine ADR 0008). Nothing wrote the table after life_notify went.
DROP TABLE IF EXISTS life.notices;
