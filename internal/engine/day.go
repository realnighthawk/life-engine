package engine

import (
	"context"
	"sort"
	"time"
)

// The day is a view, not a record (project-hub ADR 0008): the tasks and events that fall on it, one entry per occurrence,
// and the open tasks from earlier days that are still waiting. Nothing is stored for it, so there is nothing to close.
type DayInput struct {
	Day     string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD in the person's time zone; default today"`
	Subject string `json:"subject,omitempty" jsonschema:"person (default) or agent"`
}
type DayEntry struct {
	Task  *Task          `json:"task,omitempty" jsonschema:"set for a task entry"`
	Event *CalendarEvent `json:"event,omitempty" jsonschema:"set for an event entry"`
	At    time.Time      `json:"at" jsonschema:"when this occurrence falls"`
	State string         `json:"state" jsonschema:"tasks: open or done. Events: upcoming or past"`
	// Overdue marks an open one-off task from an earlier day, carried onto today.
	Overdue bool `json:"overdue"`
}
type Day struct {
	Day     string     `json:"day"`
	Entries []DayEntry `json:"entries" jsonschema:"tasks and events timed on this day, earliest first, then the overdue ones"`
}

func dayView(ctx context.Context, u *unit, in DayInput) (Day, error) {
	today, loc, now, e := u.today(ctx)
	if e != nil {
		return Day{}, e
	}
	d := today
	if in.Day != "" {
		if d, e = day("day", in.Day); e != nil {
			return Day{}, e
		}
	}
	subject := in.Subject
	if subject == "" {
		subject = "person"
	}
	if !oneOf(subject, "person", "agent") {
		return Day{}, invalid("subject must be person or agent")
	}
	start, _ := time.ParseInLocation("2006-01-02", d, loc)
	end := start.AddDate(0, 0, 1)
	out := Day{Day: d, Entries: []DayEntry{}}

	var tasks []Task
	rows, e := u.tx.Query(ctx, "SELECT "+taskCols+` FROM life.tasks WHERE deleted_at IS NULL AND subject=$1 AND at IS NOT NULL AND
		(recurrence IS NOT NULL OR (at >= $2 AND at < $3) OR (done_at IS NULL AND at < $2))`, subject, start, end)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}

	var events []CalendarEvent
	rows, e = u.tx.Query(ctx, "SELECT "+eventCols+` FROM life.events WHERE deleted_at IS NULL AND cancelled_at IS NULL AND subject=$1
		AND at IS NOT NULL AND (recurrence IS NOT NULL OR (at >= $2 AND at < $3))`, subject, start, end)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		ev, e := scanEvent(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		events = append(events, ev)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}

	// Which task occurrences an activity proves done, in this day.
	cleared := map[string]bool{}
	crows, e := u.tx.Query(ctx, `SELECT l.to_id::text, a.occurred_at FROM life.activities a
		JOIN life.links l ON l.from_kind='activity' AND l.from_id=a.id
		WHERE l.relation='fulfils' AND l.to_kind='task' AND a.occurred_at >= $1 AND a.occurred_at < $2`, start, end)
	if e != nil {
		return out, e
	}
	for crows.Next() {
		var taskID string
		var at time.Time
		if e = crows.Scan(&taskID, &at); e != nil {
			crows.Close()
			return out, e
		}
		cleared[taskID+"|"+at.UTC().Format(time.RFC3339)] = true
	}
	crows.Close()

	for _, it := range tasks {
		it := it
		var times []time.Time
		overdue := false
		switch {
		case it.Recurrence != nil:
			rloc, _ := zone(it.TimeZone)
			times = occurrencesIn(*it.At, *it.Recurrence, rloc, start, end)
		case !it.At.Before(start) && it.At.Before(end):
			times = []time.Time{*it.At}
		case d == today && it.DoneAt == nil && it.At.Before(start):
			times, overdue = []time.Time{*it.At}, true
		}
		for _, at := range times {
			state := "open"
			if cleared[it.ID+"|"+at.UTC().Format(time.RFC3339)] || (it.Recurrence == nil && it.DoneAt != nil) {
				state = "done"
			}
			out.Entries = append(out.Entries, DayEntry{Task: &it, At: at.UTC(), State: state, Overdue: overdue})
		}
	}
	for _, ev := range events {
		ev := ev
		var times []time.Time
		if ev.Recurrence != nil {
			rloc, _ := zone(ev.TimeZone)
			times = occurrencesIn(ev.At, *ev.Recurrence, rloc, start, end)
		} else if !ev.At.Before(start) && ev.At.Before(end) {
			times = []time.Time{ev.At}
		}
		for _, at := range times {
			state := "past"
			if at.After(now) {
				state = "upcoming"
			}
			out.Entries = append(out.Entries, DayEntry{Event: &ev, At: at.UTC(), State: state})
		}
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Overdue != b.Overdue {
			return !a.Overdue
		}
		return a.At.Before(b.At)
	})
	return out, nil
}
