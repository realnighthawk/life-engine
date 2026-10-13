-- A repeating reminder is a series; each occurrence is cleared on its own. series_anchor is where the series started (it does
-- not move when due_at advances), and reminder_acks records the occurrences that have been cleared. See ADR 0004.
ALTER TABLE life.reminders ADD COLUMN series_anchor timestamptz;
UPDATE life.reminders SET series_anchor = due_at WHERE recurrence IS NOT NULL;
CREATE TABLE life.reminder_acks (
  reminder_id   uuid NOT NULL REFERENCES life.reminders(id),
  occurrence_at timestamptz NOT NULL,
  acked_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (reminder_id, occurrence_at)
);
