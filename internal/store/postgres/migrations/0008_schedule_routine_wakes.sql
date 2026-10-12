-- The instant each record's wake (a Temporal Schedule) is armed for; null when none is. See ADR 0003.
ALTER TABLE life.schedules ADD COLUMN armed_for timestamptz;
ALTER TABLE life.routines ADD COLUMN armed_for timestamptz;
