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
	Item  Item      `json:"item"`
	At    time.Time `json:"at" jsonschema:"when this occurrence falls"`
	State string    `json:"state" jsonschema:"tasks: open or done. Events: upcoming or past"`
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

	// Everything timed that could fall on the day: one-offs on it, anything repeating, and (for today) open one-off tasks
	// from before it.
	rows, e := u.tx.Query(ctx, "SELECT "+itemCols+` FROM life.items WHERE deleted_at IS NULL AND subject=$1 AND at IS NOT NULL AND
		(recurrence IS NOT NULL OR (at >= $2 AND at < $3) OR (kind='task' AND done_at IS NULL AND at < $2))`, subject, start, end)
	if e != nil {
		return out, e
	}
	var items []Item
	for rows.Next() {
		it, e := scanItem(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		items = append(items, it)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	cleared := map[string]bool{}
	crows, e := u.tx.Query(ctx, `SELECT item_id::text, at FROM life.occurrences WHERE at >= $1 AND at < $2`, start, end)
	if e != nil {
		return out, e
	}
	for crows.Next() {
		var itemID string
		var at time.Time
		if e = crows.Scan(&itemID, &at); e != nil {
			crows.Close()
			return out, e
		}
		cleared[itemID+"|"+at.UTC().Format(time.RFC3339)] = true
	}
	crows.Close()

	for _, it := range items {
		var times []time.Time
		overdue := false
		switch {
		case it.Recurrence != nil:
			rloc, _ := zone(it.TimeZone)
			times = occurrencesIn(*it.At, *it.Recurrence, rloc, start, end)
		case !it.At.Before(start) && it.At.Before(end):
			times = []time.Time{*it.At}
		case d == today && it.Kind == "task" && it.DoneAt == nil && it.At.Before(start):
			times, overdue = []time.Time{*it.At}, true
		}
		for _, at := range times {
			out.Entries = append(out.Entries, DayEntry{Item: it, At: at.UTC(), State: entryState(it, at, now, cleared[it.ID+"|"+at.UTC().Format(time.RFC3339)]), Overdue: overdue})
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

func entryState(it Item, at, now time.Time, cleared bool) string {
	if it.Kind == "event" {
		if at.After(now) {
			return "upcoming"
		}
		return "past"
	}
	if cleared || (it.Recurrence == nil && it.DoneAt != nil) {
		return "done"
	}
	return "open"
}
