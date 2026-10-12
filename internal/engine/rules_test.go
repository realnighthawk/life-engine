package engine

import (
	"testing"
	"time"
)

func now() *time.Time { t := time.Now(); return &t }

func TestSuggestNext(t *testing.T) {
	items := []PlanItem{
		{ID: "a", Title: "Draft memo", Minutes: 40, IsPriority: true, Kind: "task"},
		{ID: "b", Title: "Reply to Sam", Minutes: 10, Kind: "task"},
		{ID: "c", Title: "Standup", Minutes: 15, Kind: "event"},
		{ID: "d", Title: "Done thing", Minutes: 5, Kind: "task", DoneAt: now()},
	}
	step := &Step{ID: "s1", Title: "Lesson 1", Minutes: 20}
	goals := []Goal{{ID: "g1", Title: "Learn Spanish", NextStep: step}}

	if s := suggestNext(45, items, goals); s == nil || s.Title != "Draft memo" || s.Source != "plan_item" {
		t.Errorf("a priority that fits wins: %+v", s)
	}
	if s := suggestNext(20, items, goals); s == nil || s.Title != "Lesson 1" || s.GoalID != "g1" || s.StepID != "s1" {
		t.Errorf("when the priority is too long, the goal step is next: %+v", s)
	}
	if s := suggestNext(10, items, goals); s == nil || s.Title != "Reply to Sam" {
		t.Errorf("otherwise anything that fits: %+v", s)
	}
	if s := suggestNext(5, items, goals); s != nil {
		t.Errorf("events and done items are never suggested, nothing fits 5 minutes: %+v", s)
	}
	if s := suggestNext(60, nil, nil); s != nil {
		t.Errorf("empty plan: %+v", s)
	}
	archived := now()
	if s := suggestNext(20, nil, []Goal{{ID: "g", NextStep: step, ArchivedAt: archived}}); s != nil {
		t.Errorf("archived goals are skipped: %+v", s)
	}
	longer := []PlanItem{{ID: "x", Title: "B", Minutes: 20, IsPriority: true}, {ID: "y", Title: "A", Minutes: 30, IsPriority: true}}
	if s := suggestNext(45, longer, nil); s.Title != "A" {
		t.Errorf("the longest priority that fits uses the time: %+v", s)
	}
}

func TestReplan(t *testing.T) {
	items := []PlanItem{
		{ID: "1", Title: "Draft memo", Minutes: 40, IsPriority: true, Kind: "task"},
		{ID: "2", Title: "Reply to Sam", Minutes: 10, Kind: "task"},
		{ID: "3", Title: "Standup", Minutes: 15, Kind: "event"},
		{ID: "4", Title: "Finished", Minutes: 60, Kind: "task", DoneAt: now()},
		{ID: "5", Title: "Gone", Minutes: 5, Kind: "task", DeletedAt: now()},
	}
	r := replan(items, 30)
	keep := map[string]bool{}
	for _, it := range r.Keep {
		keep[it.ID] = true
	}
	if !keep["2"] || !keep["3"] || !keep["4"] || keep["1"] || keep["5"] {
		t.Errorf("30 minutes keeps the short task, the event and finished work: %+v", r.Keep)
	}
	if len(r.MoveToTomorrow) != 1 || r.MoveToTomorrow[0].ID != "1" || r.MinutesKept != 10 {
		t.Errorf("the priority that does not fit moves to tomorrow: %+v", r)
	}
	if r := replan(items, 60); len(r.MoveToTomorrow) != 0 {
		t.Errorf("60 minutes fits everything open: %+v", r)
	}
	if r := replan(items, 0); len(r.MoveToTomorrow) != 2 {
		t.Errorf("0 minutes moves all open work: %+v", r)
	}
}

func TestCloseDay(t *testing.T) {
	c := closeDay(nil)
	if c.Planned != 0 || c.Summary != "A quiet day. Nothing was planned." {
		t.Errorf("empty: %+v", c)
	}
	c = closeDay([]PlanItem{{ID: "1", Title: "A", Kind: "task", DoneAt: now()}, {ID: "2", Title: "B", Kind: "task", IsPriority: true}, {ID: "3", Title: "Meeting", Kind: "event"}})
	if c.Done != 1 || c.Planned != 2 || len(c.Carried) != 1 || c.TomorrowNote != `Start tomorrow with "B".` {
		t.Errorf("mixed: %+v", c)
	}
	c = closeDay([]PlanItem{{Title: "A", Kind: "task", DoneAt: now()}})
	if c.Summary != "You finished all 1. Well done." || c.TomorrowNote != "Repeat what worked today." {
		t.Errorf("all done: %+v", c)
	}
	c = closeDay([]PlanItem{{Title: "A", Kind: "task"}})
	if c.Summary != "Nothing got done today, and that's allowed. Tomorrow starts clean." {
		t.Errorf("none done: %+v", c)
	}
	many := []PlanItem{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}}
	if c = closeDay(many); c.TomorrowNote != "Tomorrow, choose only your top three. The rest can wait." {
		t.Errorf("overloaded: %+v", c)
	}
}

func TestStreakAndDays(t *testing.T) {
	today := toDayNumber("2026-10-14") // a Wednesday
	if weekdayOf(today) != 3 {
		t.Fatalf("weekday %d", weekdayOf(today))
	}
	if fromDayNumber(today) != "2026-10-14" || addDays("2026-10-31", 1) != "2026-11-01" {
		t.Fatal("day arithmetic")
	}
	if mondayOf("2026-10-14") != "2026-10-12" || mondayOf("2026-10-18") != "2026-10-12" || mondayOf("2026-10-12") != "2026-10-12" {
		t.Fatal("mondayOf")
	}
	everyDay := func(dayNumber) bool { return true }
	done := map[dayNumber]bool{today - 1: true, today - 2: true, today - 4: true}
	if n := streak(today, everyDay, done); n != 2 {
		t.Errorf("open today does not break the streak, a missed day does: %d", n)
	}
	done[today] = true
	if n := streak(today, everyDay, done); n != 3 {
		t.Errorf("done today extends it: %d", n)
	}
	weekdaysOnly := func(d dayNumber) bool { w := weekdayOf(d); return w >= 1 && w <= 5 }
	fri, mon := toDayNumber("2026-10-09"), toDayNumber("2026-10-12")
	if n := streak(mon, weekdaysOnly, map[dayNumber]bool{fri: true, mon: true}); n != 2 {
		t.Errorf("a weekend neither extends nor breaks: %d", n)
	}
}
