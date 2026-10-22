package engine

import "context"

// Descriptions are written for the agent to choose a tool by outcome, so they say what the person gets, not how it is stored.
func (s *Service) registerOperations() {
	register(s, "goals_create", "Create a goal: an OUTCOME the person wants (\"run a 10k by March\", \"Lisbon trip, June 3-10\"). Give it an end date if it finishes, leave it open if it does not. A goal is also the container for a composed use case: for a trip, a dinner or a move, create the goal first, then create the tasks and events that serve it with this goal's id. Do not copy a task or event into a goal; link it with goal_id.", true, createGoal)
	register(s, "goals_get", "Read a goal with its progress (done and total tasks linked to it) and its next open task.", false, func(c context.Context, u *unit, in GetInput) (Goal, error) { return u.getGoal(c, in.ID, false) })
	register(s, "goals_list", "List the person's goals with their progress and next task, soonest end date first. State deleted means archived.", false, listGoals)
	register(s, "goals_update", "Replace a goal's title, notes, end date and subject using the version you last read.", true, updateGoal)
	register(s, "goals_archive", "Archive a goal (it can be restored). What is linked to it stays.", true, func(c context.Context, u *unit, in EditInput) (Goal, error) { return lifecycleGoal(c, u, in, false) })
	register(s, "goals_restore", "Restore an archived goal.", true, func(c context.Context, u *unit, in EditInput) (Goal, error) { return lifecycleGoal(c, u, in, true) })

	register(s, "tasks_create", "Create a task: something that waits for someone to DO it. A time on it is a deadline or a cue (\"call Sam at 5\"), not an appointment. To be reminded, give it a time and alert_before_minutes (0 = at that time); a reminder is just a task with an alert. To make it repeat, give it recurrence; a habit or routine is a task that repeats. Link it to a goal with goal_id when it serves one. For something that happens at a time whether or not anyone acts (a meeting, a flight), use calendar_events_create instead.", true, createItem("task"))
	register(s, "tasks_get", "Read one task.", false, func(c context.Context, u *unit, in GetInput) (Item, error) { return u.getItem(c, "task", in.ID, false) })
	register(s, "tasks_list", "List tasks: open by default, or done or all; optionally for one goal, within a time range or matching text. Soonest first, untimed last. Your own tasks are subject agent.", false, listItems("task"))
	register(s, "tasks_update", "Replace a task's fields using the version you last read. Changing its time clears any snooze.", true, updateItem("task"))
	register(s, "tasks_complete", "Mark a task done. A repeating task carries on: only that occurrence is done (name it with at, else the one now due). For your own tasks, pass result: one line on what you found or did.", true, completeTask)
	register(s, "tasks_snooze", "Defer a task's alert to a later time without changing the task.", true, snoozeItem("task"))
	register(s, "tasks_delete", "Delete a task (it can be restored).", true, lifecycleItem("task", false))
	register(s, "tasks_restore", "Restore a deleted task.", true, lifecycleItem("task", true))

	register(s, "calendar_events_create", "Create an event: something that HAPPENS at a time whether or not anyone acts, and the person shows up (a meeting, a flight, a dinner). It has a start time and optionally a length; it is not something to complete. Give alert_before_minutes to be told ahead of it, and recurrence to repeat it. Link it to a goal with goal_id when it serves one. For something to DO, use tasks_create.", true, createItem("event"))
	register(s, "calendar_events_get", "Read one event.", false, func(c context.Context, u *unit, in GetInput) (Item, error) {
		return u.getItem(c, "event", in.ID, false)
	})
	register(s, "calendar_events_list", "List events, soonest first; optionally for one goal, within a time range or matching text.", false, listItems("event"))
	register(s, "calendar_events_update", "Replace an event's fields using the version you last read. Changing its time clears any snooze.", true, updateItem("event"))
	register(s, "calendar_events_snooze", "Defer an event's alert to a later time without changing the event.", true, snoozeItem("event"))
	register(s, "calendar_events_delete", "Delete an event (it can be restored).", true, lifecycleItem("event", false))
	register(s, "calendar_events_restore", "Restore a deleted event.", true, lifecycleItem("event", true))

	register(s, "day_get", "A day's tasks and events, one entry per occurrence, earliest first, followed by the open tasks from earlier days that are still waiting (when the day is today). This is the person's Today screen; there is no stored plan. Defaults to today in the person's time zone.", false, dayView)

	register(s, "batch_apply", "Create a plan's records together, all or nothing: goals, tasks and events. Operations run in order, and each new record carries an id you chose so a later operation can refer to it (a task's goal_id). Use it for a composed use case such as a trip: the goal, then the tasks and events that serve it. Pass an idempotency_key; retrying with it is safe.", true, s.batchApply)
	register(s, "batch_undo", "Undo a batch_apply plan as a whole: archives the goals and deletes the tasks and events it created, newest first, in one transaction. Pass the idempotency_key the batch_apply used. Records already removed are skipped.", true, s.batchUndo)
	registerKeyless(s, "batch_dry_run", "Run a plan exactly as batch_apply would, then undo it, so every rule is checked and nothing is created. Use it on the same operations before batch_apply.", s.batchDryRun)

	register(s, "settings_get", "Read the person's settings: their time zone (which decides what 'today' means) and working hours.", false, func(c context.Context, u *unit, _ struct{}) (Settings, error) { return u.getSettings(c) })
	register(s, "settings_set", "Set the person's time zone and working hours (versioned). Set the time zone first: what today is depends on it.", true, setSettings)
	register(s, "history_list", "Read the audit trail of changes to a record, newest first.", false, history)
	register(s, "life_notify", "Tell the person something outside the conversation: it reaches this app as a notification (and its inbox), and no other app. Use it sparingly, for something they would want to know now or would miss; never for chatter or progress.", true, notifyPerson)
	register(s, "events_pending", "Hand over the notices this engine has recorded and not yet delivered. Called by the hub's delivery loop, not by the agent. (Not calendar events.)", false,
		func(c context.Context, u *unit, _ struct{}) (EventsResult, error) { return u.eventsPending(c) })
	registerKeyless(s, "events_ack", "Mark notices delivered so they are not handed over twice. Called by the hub's delivery loop, not by the agent.",
		func(c context.Context, u *unit, in AckEventsInput) (AckEventsResult, error) {
			return u.eventsAck(c, in)
		})
}
