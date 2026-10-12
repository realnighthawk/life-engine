-- The instant a reminder's wake (a Temporal Schedule) is armed for; null when none is. See ADR 0003.
ALTER TABLE life.reminders ADD COLUMN armed_for timestamptz;
