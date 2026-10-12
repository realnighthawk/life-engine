package engine

import "context"

// Descriptions are written for the agent to choose a tool by outcome, so they say what the person gets, not how it is stored.
func (s *Service) registerOperations() {
	register(s, "reminders_create", "Create a reminder for the person: something to be reminded of, optionally at a time and optionally repeating (daily, weekly on chosen weekdays, or monthly). Use this whenever the person asks to be reminded, instead of arming a private wake. It appears in their app, and when it is due they are notified without you being woken. Set source to agent and pass session_id when you create it.", true, createReminder)
	register(s, "reminders_get", "Read one reminder, including whether it is done, snoozed or repeating.", false, func(c context.Context, u *unit, in GetInput) (Reminder, error) { return u.getReminder(c, in.ID, false) })
	register(s, "reminders_list", "List the person's reminders: pending by default, or done or all, optionally within a due range or matching text. Soonest first; undated last.", false, listReminders)
	register(s, "reminders_update", "Replace a reminder's editable fields (title, notes, due time, zone, repeat, link) using the version you last read. Changing the due time clears any snooze.", true, updateReminder)
	register(s, "reminders_complete", "Mark a reminder done. A repeating reminder stays pending and moves to its next occurrence if it was due.", true, completeReminder)
	register(s, "reminders_snooze", "Be reminded again at a later time without changing the reminder's schedule.", true, snoozeReminder)
	register(s, "reminders_delete", "Delete a reminder (it can be restored).", true, func(c context.Context, u *unit, in EditInput) (Reminder, error) {
		return lifecycleReminder(c, u, in, false)
	})
	register(s, "reminders_restore", "Restore a deleted reminder.", true, func(c context.Context, u *unit, in EditInput) (Reminder, error) {
		return lifecycleReminder(c, u, in, true)
	})
	register(s, "settings_get", "Read the person's settings: their time zone (which decides what 'today' means) and working hours.", false, func(c context.Context, u *unit, _ struct{}) (Settings, error) { return u.getSettings(c) })
	register(s, "settings_set", "Set the person's time zone and working hours (versioned). Set the time zone first: today's plan and closing the day depend on it.", true, setSettings)
	register(s, "schedules_create", "Create a recurring prompt the person asked you to run, such as 'every Sunday evening, review my week'. It appears in their app where they can pause or edit it. Use this, not a private wake, for anything the person asked to happen on a schedule; use a wake only for your own follow-ups. When it fires you are woken with the instruction.", true, createSchedule)
	register(s, "schedules_get", "Read one schedule, including when it next fires.", false, func(c context.Context, u *unit, in GetInput) (Schedule, error) { return u.getSchedule(c, in.ID, false) })
	register(s, "schedules_list", "List the person's recurring prompts, soonest first.", false, listSchedules)
	register(s, "schedules_update", "Replace a schedule's name, instruction, time, repeat or enabled flag using the version you last read.", true, updateSchedule)
	register(s, "schedules_delete", "Delete a schedule (it can be restored).", true, func(c context.Context, u *unit, in EditInput) (Schedule, error) {
		return lifecycleSchedule(c, u, in, false)
	})
	register(s, "schedules_restore", "Restore a deleted schedule; it resumes from now.", true, func(c context.Context, u *unit, in EditInput) (Schedule, error) {
		return lifecycleSchedule(c, u, in, true)
	})
	register(s, "history_list", "Read the audit trail of changes to a record, newest first.", false, history)
	register(s, "events_pending", "Hand over the notices this engine has recorded and not yet delivered. Called by the hub's delivery loop, not by the agent.", false,
		func(c context.Context, u *unit, _ struct{}) (EventsResult, error) { return u.eventsPending(c) })
	registerKeyless(s, "events_ack", "Mark notices delivered so they are not handed over twice. Called by the hub's delivery loop, not by the agent.",
		func(c context.Context, u *unit, in AckEventsInput) (AckEventsResult, error) {
			return u.eventsAck(c, in)
		})
}
