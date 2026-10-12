package engine

import (
	"context"
	"strings"
	"time"
)

// ---- close the day

type DayCloseInput struct {
	Meta
	Day        string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
	Reflection string `json:"reflection,omitempty" jsonschema:"one or two lines about the day; saved to the journal with tomorrow's adjustment"`
}
type DayCloseResult struct {
	DayClose
	Day            string `json:"day"`
	NextDay        string `json:"next_day"`
	JournalEntryID string `json:"journal_entry_id,omitempty"`
}

// dayClose records the day: unfinished work moves to the next day, and a reflection (if given) becomes a journal entry
// carrying tomorrow's suggested adjustment. Closing a day twice moves nothing more, because nothing is left to carry.
func dayClose(ctx context.Context, u *unit, in DayCloseInput) (DayCloseResult, error) {
	d, e := u.dayOrToday(ctx, in.Day)
	if e != nil {
		return DayCloseResult{}, e
	}
	reflection := strings.TrimSpace(in.Reflection)
	if len(reflection) > 20000 {
		return DayCloseResult{}, invalid("reflection must be at most 20000 characters")
	}
	items, e := u.planItemsOn(ctx, d)
	if e != nil {
		return DayCloseResult{}, e
	}
	out := DayCloseResult{DayClose: closeDay(items), Day: d, NextDay: addDays(d, 1)}
	for _, it := range out.Carried {
		if _, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET day=$2,start_at=NULL,version=version+1,updated_at=now() WHERE id=$1`, it.ID, out.NextDay); e != nil {
			return out, e
		}
		if _, e = u.planItemChanged(ctx, it); e != nil {
			return out, e
		}
	}
	if reflection != "" {
		var jid string
		note := out.TomorrowNote
		if e = u.tx.QueryRow(ctx, `INSERT INTO life.journal_entries(text,tomorrow,source) VALUES($1,$2,'day_close') RETURNING id::text`, reflection, note).Scan(&jid); e != nil {
			return out, e
		}
		entry, e := u.getJournal(ctx, jid, false)
		if e != nil {
			return out, e
		}
		if e = u.audit(ctx, "journal_entry", jid, nil, entry); e != nil {
			return out, e
		}
		out.JournalEntryID = jid
	}
	return out, nil
}

// ---- week stats

type WeekStatsInput struct {
	WeekStart string `json:"week_start,omitempty" jsonschema:"the local Monday, YYYY-MM-DD; default this week"`
}
type WeekStats struct {
	WeekStart        string `json:"week_start"`
	Planned          int    `json:"planned" jsonschema:"non-event plan items on the week's days"`
	Done             int    `json:"done"`
	StepsDone        int    `json:"steps_done"`
	JournalEntries   int    `json:"journal_entries"`
	RoutineCheckins  int    `json:"routine_checkins"`
	CommitmentsTotal int    `json:"commitments_total"`
	CommitmentsDone  int    `json:"commitments_done"`
	Change           string `json:"change" jsonschema:"one suggested change for next week, from what was planned, finished and written"`
}

func weekStats(ctx context.Context, u *unit, in WeekStatsInput) (WeekStats, error) {
	week, e := u.weekStart(ctx, in.WeekStart)
	if e != nil {
		return WeekStats{}, e
	}
	s, e := u.getSettings(ctx)
	if e != nil {
		return WeekStats{}, e
	}
	end := addDays(week, 6)
	w := WeekStats{WeekStart: week}
	row := func(q string, dest ...any) error { return u.tx.QueryRow(ctx, q, week, end, s.TimeZone).Scan(dest...) }
	if e = row(`SELECT count(*), count(done_at) FROM life.plan_items WHERE deleted_at IS NULL AND kind<>'event' AND day BETWEEN $1::date AND $2::date AND $3<>''`, &w.Planned, &w.Done); e != nil {
		return w, e
	}
	if e = row(`SELECT count(*) FROM life.steps WHERE done_at IS NOT NULL AND (done_at AT TIME ZONE $3)::date BETWEEN $1::date AND $2::date`, &w.StepsDone); e != nil {
		return w, e
	}
	if e = row(`SELECT count(*) FROM life.journal_entries WHERE deleted_at IS NULL AND (entry_at AT TIME ZONE $3)::date BETWEEN $1::date AND $2::date`, &w.JournalEntries); e != nil {
		return w, e
	}
	if e = row(`SELECT count(*) FROM life.routine_checkins WHERE day BETWEEN $1::date AND $2::date AND $3<>''`, &w.RoutineCheckins); e != nil {
		return w, e
	}
	if e = row(`SELECT count(*), count(done_at) FROM life.commitments WHERE week_start=$1::date AND $2<>'' AND $3<>''`, &w.CommitmentsTotal, &w.CommitmentsDone); e != nil {
		return w, e
	}
	w.Change = weekChange(w.Planned, w.Done, w.JournalEntries)
	return w, nil
}

// ---- context for the agent

type LifeContextInput struct {
	Scope     string `json:"scope" jsonschema:"day, week or goal"`
	Day       string `json:"day,omitempty" jsonschema:"for scope day: local date YYYY-MM-DD, default today"`
	WeekStart string `json:"week_start,omitempty" jsonschema:"for scope week: the local Monday, default this week"`
	GoalID    string `json:"goal_id,omitempty" jsonschema:"required for scope goal"`
}
type JournalExcerpt struct {
	ID       string    `json:"id"`
	EntryAt  time.Time `json:"entry_at"`
	Text     string    `json:"text" jsonschema:"truncated to 500 characters"`
	Tomorrow *string   `json:"tomorrow"`
}
type ContextSource struct {
	EntityType string    `json:"entity_type"`
	ID         string    `json:"id"`
	Version    int64     `json:"version"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type LifeContext struct {
	SchemaVersion int              `json:"schema_version"`
	Scope         string           `json:"scope"`
	RetrievedAt   time.Time        `json:"retrieved_at"`
	TimeZone      string           `json:"time_zone"`
	Today         string           `json:"today"`
	Coverage      string           `json:"coverage" jsonschema:"bounded_snapshot: the lists below are limited and may not be everything"`
	Plan          *PlanDay         `json:"plan"`
	Commitments   *CommitmentsWeek `json:"commitments"`
	Goals         []Goal           `json:"goals"`
	RemindersDue  []Reminder       `json:"reminders_due" jsonschema:"pending reminders overdue or due within a day, at most 10"`
	Routines      *RoutinesStatus  `json:"routines"`
	RecentJournal []JournalExcerpt `json:"recent_journal" jsonschema:"the last three entries"`
	Week          *WeekStats       `json:"week"`
	Sources       []ContextSource  `json:"sources" jsonschema:"every record above with its version and last change, so a write can name what it read"`
}

func lifeContext(ctx context.Context, u *unit, in LifeContextInput) (LifeContext, error) {
	out := LifeContext{SchemaVersion: 1, Scope: in.Scope, Coverage: "bounded_snapshot", Goals: []Goal{}, RemindersDue: []Reminder{}, RecentJournal: []JournalExcerpt{}, Sources: []ContextSource{}}
	if !oneOf(in.Scope, "day", "week", "goal") {
		return out, invalid("scope must be day, week or goal")
	}
	today, loc, now, e := u.today(ctx)
	if e != nil {
		return out, e
	}
	out.RetrievedAt, out.Today, out.TimeZone = now.UTC(), today, loc.String()
	src := func(kind, id string, v int64, at time.Time) {
		out.Sources = append(out.Sources, ContextSource{EntityType: kind, ID: id, Version: v, UpdatedAt: at})
	}

	switch in.Scope {
	case "goal":
		if in.GoalID == "" {
			return out, invalid("goal_id is required for scope goal")
		}
		g, e := u.getGoal(ctx, in.GoalID, false)
		if e != nil {
			return out, e
		}
		out.Goals = []Goal{g}
	default:
		goals, e := u.goals(ctx, "WHERE archived_at IS NULL ORDER BY created_at, id LIMIT 10")
		if e != nil {
			return out, e
		}
		if goals != nil {
			out.Goals = goals
		}
	}
	for _, g := range out.Goals {
		src("goal", g.ID, g.Version, g.UpdatedAt)
	}

	if in.Scope == "day" || in.Scope == "week" {
		d := today
		if in.Scope == "day" && in.Day != "" {
			if d, e = day("day", in.Day); e != nil {
				return out, e
			}
		}
		plan, e := u.planDay(ctx, d)
		if e != nil {
			return out, e
		}
		out.Plan = &plan
		for _, it := range plan.Items {
			src("plan_item", it.ID, it.Version, it.UpdatedAt)
		}
		weekArg := in.WeekStart
		if in.Scope == "day" {
			weekArg = mondayOf(d)
		}
		cm, e := listCommitments(ctx, u, ListCommitmentsInput{WeekStart: weekArg})
		if e != nil {
			return out, e
		}
		out.Commitments = &cm
		rs, e := routinesStatus(ctx, u, RoutinesStatusInput{Day: d})
		if e != nil {
			return out, e
		}
		out.Routines = &rs
	}
	if in.Scope == "week" {
		ws, e := weekStats(ctx, u, WeekStatsInput{WeekStart: in.WeekStart})
		if e != nil {
			return out, e
		}
		out.Week = &ws
	}

	rows, e := u.tx.Query(ctx, "SELECT "+reminderCols+` FROM life.reminders WHERE deleted_at IS NULL AND state='pending' AND due_at IS NOT NULL AND due_at <= now() + interval '1 day' ORDER BY due_at, id LIMIT 10`)
	if e != nil {
		return out, e
	}
	rem, e := collect(rows, scanReminder)
	if e != nil {
		return out, e
	}
	for _, r := range rem {
		out.RemindersDue = append(out.RemindersDue, r)
		src("reminder", r.ID, r.Version, r.UpdatedAt)
	}

	rows, e = u.tx.Query(ctx, "SELECT "+journalCols+" FROM life.journal_entries WHERE deleted_at IS NULL ORDER BY entry_at DESC, id LIMIT 3")
	if e != nil {
		return out, e
	}
	entries, e := collect(rows, scanJournal)
	if e != nil {
		return out, e
	}
	for _, j := range entries {
		text := j.Text
		if r := []rune(text); len(r) > 500 {
			text = string(r[:500]) + "…"
		}
		out.RecentJournal = append(out.RecentJournal, JournalExcerpt{ID: j.ID, EntryAt: j.EntryAt, Text: text, Tomorrow: j.Tomorrow})
		src("journal_entry", j.ID, j.Version, j.UpdatedAt)
	}
	return out, nil
}
