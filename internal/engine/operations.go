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
	register(s, "tasks_complete", "Mark a task done. A repeating task carries on: only that occurrence is done (name it with at, else the one now due). For your own tasks, pass result: one line on what you found or did. Pass done false to reopen a task that was marked done.", true, completeTask)
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

	register(s, "watches_create", "Arm a watch: be woken, with a fresh turn, when an EVENT happens that an engine notices (a budget crossing its line, an arrival). Give the event_key exactly as the engine that raises it names it, and an objective: what to do when woken, since the event says only what happened. The name is the watch's identity: arming the same name again revises it. To be woken when one of your own tasks is DONE, watch the event_key \"task_done:<the task id>\" (an agent task's id is in tasks_list); you are woken with its result. To be woken at a TIME, create a task of your own with an alert instead.", true, createWatch)
	register(s, "watches_get", "Read one watch.", false, func(c context.Context, u *unit, in GetInput) (Watch, error) { return u.getWatch(c, in.ID, false) })
	register(s, "watches_list", "List your watches for this conversation, by name. State deleted means cancelled.", false, listWatches)
	register(s, "watches_update", "Change what a watch listens for, what it does or when it expires, using the version you last read. A field you leave out stays.", true, updateWatch)
	register(s, "watches_delete", "Cancel a watch. It stops firing at once.", true, deleteWatch)

	register(s, "tasks_history", "A repeating task's recent days: for each, whether an occurrence fell and how many were done, the last 7 and 30 days, and the current and best run. A missed day lowers the rate; it never resets the history to zero. Use it to see how a habit is going.", false, tasksHistory)

	register(s, "tracker_catalog_get", "Read the view kit a client supports: the blocks it can draw, the calculations it can run on live data, and how to compose them. Read it before composing a view of a goal, task or event; it names the exact catalog_id to use. The kit is finite and says nothing about kinds of goal: you compose it for the subject in front of you.", false, getTrackerCatalog)
	register(s, "views_create", "Create a view of a goal, task or event, composed from the client's view kit: metrics (data sources: a provider series, a repeating task's done occurrences, or numbers the person logs) and pages of titled sections of blocks (sentence, bar, series, grid, steps, timeline, tasks, events, log, text, link) that draw them and calculate over them. Give each section one purpose and a short intro; use several pages only when the subject has genuinely separate concerns. Read tracker_catalog_get first and use the requesting client's exact catalog_id. A view holds no data and no markup, and you never write a figure: bind to a metric and use a function. Create it as the last step of a batch_apply plan, after the subject it is a view of.", true, createView)
	register(s, "views_update", "Change a view's metrics and pages using the version last read; both replace the current ones. A metric that has recorded observations must stay, in the same unit. Use this when the person asks to change what a view shows, or when the subject has changed enough that its view should be composed again.", true, updateView)
	register(s, "views_delete", "Remove a view; the subject and its records stay.", true, deleteView)
	register(s, "views_state", "Open a subject's view for one client catalog: the view (or null) and the observations recorded against its metrics. Never substitute another app's view.", false, viewState)
	register(s, "views_get", "Read one view by id.", false, getView)
	register(s, "views_list", "List the views of a goal, task or event, optionally only the one for a client catalog.", false, listViews)
	register(s, "measurements_record", "Record an actual numeric observation for a view's defined metric, with its observed time and notes. Never invent values or treat completing a task as an outcome measurement.", true, recordMeasurement)
	register(s, "measurements_update", "Correct a recorded observation using the version last read; preserves its time unless supplied.", true, updateMeasurement)
	register(s, "measurements_delete", "Remove an erroneous observation; its audit history remains.", true, deleteMeasurement)

	register(s, "day_get", "A day's tasks and events, one entry per occurrence, earliest first, followed by the open tasks from earlier days that are still waiting (when the day is today). This is the person's Today screen; there is no stored plan. Defaults to today in the person's time zone.", false, dayView)

	register(s, "batch_apply", "Create a plan's records together, all or nothing: goals, tasks and events. Operations run in order, and each new record carries an id you chose so a later operation can refer to it (a task's goal_id). Use it for a composed use case such as a trip: the goal, then the tasks and events that serve it. Pass an idempotency_key; retrying with it is safe.", true, s.batchApply)
	register(s, "batch_undo", "Undo a batch_apply plan as a whole: archives the goals and deletes the tasks and events it created, newest first, in one transaction. Pass the idempotency_key the batch_apply used. Records already removed are skipped.", true, s.batchUndo)
	registerKeyless(s, "batch_dry_run", "Run a plan exactly as batch_apply would, then undo it, so every rule is checked and nothing is created. Use it on the same operations before batch_apply.", s.batchDryRun)

	register(s, "intentions_create", "Create an intention: a standing WANT. kind achieve (\"run a 10k\"), maintain (\"keep weekends free\") or experience (\"see the northern lights\"). It may have a target, an end date, both or neither. Use this for what the person wants; use a commitment for what they owe; use a task for what they will do. Link the tasks that serve it with links_set.", true, createIntention)
	register(s, "intentions_get", "Read one intention with its progress, read from the tasks linked to it.", false, func(c context.Context, u *unit, in GetInput) (Intention, error) {
		return u.getIntention(c, in.ID, false)
	})
	register(s, "intentions_list", "List intentions, soonest end date first, optionally by status or matching text.", false, listIntentions)
	register(s, "intentions_update", "Replace an intention's fields using the version you last read, including its status (active, satisfied or abandoned).", true, updateIntention)
	register(s, "intentions_archive", "Archive an intention (it can be restored). Links and records stay.", true, func(c context.Context, u *unit, in EditInput) (Intention, error) {
		return lifecycleIntention(c, u, in, false)
	})
	register(s, "intentions_restore", "Restore an archived intention.", true, func(c context.Context, u *unit, in EditInput) (Intention, error) {
		return lifecycleIntention(c, u, in, true)
	})

	register(s, "commitments_create", "Create a commitment: something the person OWES to another person, an institution or themselves (\"pay rent by November 1\", \"send Sam the contract\"). Give it due_at (an instant) or due_on (a local date), not both; add counterparty and consequence when they matter. A commitment is not a task: it generates the action that discharges it. Completing it records an activity.", true, createCommitment)
	register(s, "commitments_get", "Read one commitment.", false, func(c context.Context, u *unit, in GetInput) (Commitment, error) {
		return u.getCommitment(c, in.ID, false)
	})
	register(s, "commitments_list", "List commitments, soonest due first, optionally by status or matching text.", false, listCommitments)
	register(s, "commitments_update", "Replace a commitment's fields using the version you last read.", true, updateCommitment)
	register(s, "commitments_complete", "Mark a commitment met. Records an activity that fulfils it, in the same transaction.", true, func(c context.Context, u *unit, in CommitmentStatusInput) (Commitment, error) {
		return setCommitmentStatus(c, u, in, "met")
	})
	register(s, "commitments_miss", "Mark a commitment missed, with a note on why.", true, func(c context.Context, u *unit, in CommitmentStatusInput) (Commitment, error) {
		return setCommitmentStatus(c, u, in, "missed")
	})
	register(s, "commitments_renegotiate", "Mark a commitment renegotiated, with the new terms in the note.", true, func(c context.Context, u *unit, in CommitmentStatusInput) (Commitment, error) {
		return setCommitmentStatus(c, u, in, "renegotiated")
	})
	register(s, "commitments_waive", "Waive a commitment: it no longer applies. Records why.", true, func(c context.Context, u *unit, in CommitmentStatusInput) (Commitment, error) {
		return setCommitmentStatus(c, u, in, "waived")
	})
	register(s, "commitments_delete", "Delete a commitment (it can be restored).", true, func(c context.Context, u *unit, in EditInput) (Commitment, error) {
		return lifecycleCommitment(c, u, in, false)
	})
	register(s, "commitments_restore", "Restore a deleted commitment.", true, func(c context.Context, u *unit, in EditInput) (Commitment, error) {
		return lifecycleCommitment(c, u, in, true)
	})

	register(s, "decisions_create", "Create a decision: a choice the person is facing or has made. Give 2-10 options. kind change makes it a promotion gate: give effect (a batch plan, the same shape as batch_apply) and an option with id accept; choosing accept applies the batch atomically. A change derived from memory (derived_from set) can only be resolved with a person-sourced confirmation.", true, createDecision)
	register(s, "decisions_get", "Read one decision.", false, func(c context.Context, u *unit, in GetInput) (Decision, error) { return u.getDecision(c, in.ID, false) })
	register(s, "decisions_list", "List decisions, soonest expiry first, optionally by status or matching text.", false, listDecisions)
	register(s, "decisions_update", "Replace a decision's question, options, effect or expiry while it is still open.", true, updateDecision)
	register(s, "decisions_resolve", "Record the option the person chose. For a change, choosing accept applies its effect in the same transaction. A change that came from memory needs confirmation {source: client or turn, ref}.", true, func(c context.Context, u *unit, in ResolveDecisionInput) (Decision, error) {
		return resolveDecision(c, u, s, in)
	})
	register(s, "decisions_defer", "Defer a decision, optionally with the instant it becomes open again.", true, deferDecision)
	register(s, "decisions_expire", "Expire a decision that no longer needs an answer.", true, expireDecision)

	register(s, "states_create", "Record a confirmed state: what is currently true about the person, their environment or their resources. kind fact, preference, constraint or need; topic and predicate name it; value is any JSON. Never record an inference as a state: a hypothesis stays in memory until the person confirms it.", true, createState)
	register(s, "states_get", "Read one state record.", false, func(c context.Context, u *unit, in GetInput) (State, error) { return u.getState(c, in.ID, false) })
	register(s, "states_list", "List confirmed states, optionally by kind, topic, status or matching text.", false, listStates)
	register(s, "states_update", "Replace a state record using the version you last read, including its value and status.", true, updateState)
	register(s, "states_retract", "Retract a state that is no longer true, keeping its history.", true, retractState)
	register(s, "states_delete", "Delete a state record (it can be restored).", true, func(c context.Context, u *unit, in EditInput) (State, error) { return lifecycleState(c, u, in, false) })
	register(s, "states_restore", "Restore a deleted state record.", true, func(c context.Context, u *unit, in EditInput) (State, error) { return lifecycleState(c, u, in, true) })

	register(s, "activities_record", "Record something that HAPPENED, whether or not it was planned: \"I called the dentist\", \"ran 30 minutes\", \"paid the deposit\". Append-only and correctable. A completed task or met commitment writes one automatically; use this for what no record anticipated.", true, recordActivity)
	register(s, "activities_get", "Read one activity.", false, func(c context.Context, u *unit, in GetInput) (Activity, error) { return u.getActivity(c, in.ID) })
	register(s, "activities_list", "List what happened, newest first, optionally by actor, kind, time range or matching text.", false, listActivities)
	register(s, "activities_correct", "Correct an activity's text or time. The correction is audited.", true, correctActivity)

	register(s, "links_set", "Link two records explicitly: relation serves, fulfils, affects, constrains or evidences. Linking is idempotent. No record requires a link; this is how an activity evidences an intention, a task serves an intention, or a decision affects a commitment.", true, setLink)
	register(s, "links_remove", "Remove one explicit link.", true, removeLink)
	register(s, "links_list", "List a record's links, in either direction, optionally by relation.", false, listLinks)

	register(s, "attention_get", "What needs the person now: open commitments due today or earlier, open decisions that are not expired, and tasks whose time has passed. A deterministic read; delivery is the gateway's.", false, attentionGet)

	register(s, "settings_get", "Read the person's settings: their time zone (which decides what 'today' means) and working hours.", false, func(c context.Context, u *unit, _ struct{}) (Settings, error) { return u.getSettings(c) })
	register(s, "settings_set", "Set the person's time zone and working hours (versioned). Set the time zone first: what today is depends on it.", true, setSettings)
	register(s, "history_list", "Read the audit trail of changes to a record, newest first.", false, history)
	register(s, "events_pending", "Hand over the notices this engine has recorded and not yet delivered. Called by the hub's delivery loop, not by the agent. (Not calendar events.)", false,
		func(c context.Context, u *unit, _ struct{}) (EventsResult, error) { return u.eventsPending(c) })
	registerKeyless(s, "events_ack", "Mark notices delivered so they are not handed over twice. Called by the hub's delivery loop, not by the agent.",
		func(c context.Context, u *unit, in AckEventsInput) (AckEventsResult, error) {
			return u.eventsAck(c, in)
		})
	registerKeyless(s, "events_deliver", "Hand the engine a notice another engine raised, so the watches armed for its event_key wake their conversations. Called by the hub's delivery loop, not by the agent. Idempotent: the same event_id queues nothing twice.",
		func(c context.Context, u *unit, in DeliverEventInput) (DeliverEventResult, error) {
			return u.deliverEvent(c, in)
		})
}
