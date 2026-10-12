-- A wake lands on the session it was created from (ADR 0003). armed_session is the session the current Schedule was armed
-- on, so it can be found again to move or cancel it.
ALTER TABLE life.schedules ADD COLUMN session_id text, ADD COLUMN armed_session text;
ALTER TABLE life.routines ADD COLUMN session_id text, ADD COLUMN armed_session text;
ALTER TABLE life.reminders ADD COLUMN armed_session text;
