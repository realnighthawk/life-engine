package engine

import (
	"context"
	"time"
)

// A repeating task's history is how a habit is going, read from the occurrences that fell and the ones that were done. A
// missed day lowers the rate and never resets it to zero.
type TaskHistoryInput struct {
	ID   string `json:"id" jsonschema:"UUID of a repeating task"`
	Days int    `json:"days,omitempty" jsonschema:"how many recent days, ending today, 1-90, default 30"`
}
type TaskHistoryDay struct {
	Day       string `json:"day"`
	Scheduled bool   `json:"scheduled" jsonschema:"an occurrence fell on this day"`
	Count     int    `json:"count" jsonschema:"occurrences done"`
	Target    int    `json:"target" jsonschema:"occurrences that fell"`
	Done      bool   `json:"done"`
}
type Consistency struct {
	Done      int `json:"done"`
	Scheduled int `json:"scheduled"`
}
type TaskHistory struct {
	Task       Item             `json:"task"`
	Days       []TaskHistoryDay `json:"days" jsonschema:"oldest first, ending today"`
	Last7      Consistency      `json:"last7"`
	Last30     Consistency      `json:"last30"`
	Streak     int              `json:"streak" jsonschema:"consecutive scheduled days done, ending today (or yesterday if today is still open)"`
	BestStreak int              `json:"best_streak"`
}

func tasksHistory(ctx context.Context, u *unit, in TaskHistoryInput) (TaskHistory, error) {
	t, e := u.getItem(ctx, "task", in.ID, false)
	if e != nil {
		return TaskHistory{}, e
	}
	if t.Recurrence == nil || t.At == nil {
		return TaskHistory{}, invalid("history is for a repeating task")
	}
	days := in.Days
	if days == 0 {
		days = 30
	}
	if days < 1 || days > 90 {
		return TaskHistory{}, invalid("days must be 1-90")
	}
	today, loc, _, e := u.today(ctx)
	if e != nil {
		return TaskHistory{}, e
	}
	end, _ := time.ParseInLocation("2006-01-02", today, loc)
	end = end.AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -days)
	rloc, _ := zone(t.TimeZone)
	rows, e := u.tx.Query(ctx, `SELECT at FROM life.occurrences WHERE item_id=$1 AND state='done' AND at >= $2 AND at < $3`, t.ID, start, end)
	if e != nil {
		return TaskHistory{}, e
	}
	doneAt := map[string]int{}
	for rows.Next() {
		var at time.Time
		if e = rows.Scan(&at); e != nil {
			rows.Close()
			return TaskHistory{}, e
		}
		doneAt[at.In(loc).Format("2006-01-02")]++
	}
	rows.Close()
	scheduled := map[string]int{}
	for _, at := range occurrencesIn(*t.At, *t.Recurrence, rloc, start, end) {
		scheduled[at.In(loc).Format("2006-01-02")]++
	}
	out := TaskHistory{Task: t, Days: make([]TaskHistoryDay, 0, days)}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		target, count := scheduled[d], doneAt[d]
		out.Days = append(out.Days, TaskHistoryDay{Day: d, Scheduled: target > 0, Count: count, Target: target, Done: target > 0 && count >= target})
	}
	for i, d := range out.Days {
		if d.Scheduled {
			if len(out.Days)-i <= 30 {
				out.Last30.Scheduled++
				if d.Done {
					out.Last30.Done++
				}
			}
			if len(out.Days)-i <= 7 {
				out.Last7.Scheduled++
				if d.Done {
					out.Last7.Done++
				}
			}
		}
	}
	run, best := 0, 0
	for _, d := range out.Days {
		switch {
		case !d.Scheduled:
		case d.Done:
			run++
			best = max(best, run)
		default:
			run = 0
		}
	}
	out.BestStreak = best
	// The streak ending today, or yesterday when today is still open: an unfinished today does not break it.
	streak := 0
	for i := len(out.Days) - 1; i >= 0; i-- {
		d := out.Days[i]
		if !d.Scheduled {
			continue
		}
		if d.Done {
			streak++
			continue
		}
		if i == len(out.Days)-1 {
			continue
		}
		break
	}
	out.Streak = streak
	return out, nil
}
