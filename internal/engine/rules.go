package engine

import (
	"fmt"
	"sort"
)

// The deterministic rules behind "I have 20 minutes", "my day changed" and the routine nudges. Pure functions over
// plain values so they can be tested without a database and share their cases with the clients.

type Suggestion struct {
	Title      string `json:"title"`
	Minutes    int    `json:"minutes"`
	Reason     string `json:"reason"`
	Source     string `json:"source" jsonschema:"plan_item or goal_step"`
	PlanItemID string `json:"plan_item_id,omitempty"`
	GoalID     string `json:"goal_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
}

// suggestNext picks what fits the time available: today's open priorities first, then the next step of a goal, then any
// other open item; within a tier the longest that fits, so the time is used. Ties break on title then id for stability.
func suggestNext(minutes int, items []PlanItem, goals []Goal) *Suggestion {
	var open []PlanItem
	for _, it := range items {
		if it.DoneAt == nil && it.DeletedAt == nil && it.Kind != "event" && it.Minutes <= minutes {
			open = append(open, it)
		}
	}
	longest := func(list []PlanItem) *PlanItem {
		if len(list) == 0 {
			return nil
		}
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Minutes != list[j].Minutes {
				return list[i].Minutes > list[j].Minutes
			}
			if list[i].Title != list[j].Title {
				return list[i].Title < list[j].Title
			}
			return list[i].ID < list[j].ID
		})
		return &list[0]
	}
	fromItem := func(it *PlanItem, why string) *Suggestion {
		return &Suggestion{Title: it.Title, Minutes: it.Minutes, Reason: why, Source: "plan_item", PlanItemID: it.ID}
	}
	var priorities []PlanItem
	for _, it := range open {
		if it.IsPriority {
			priorities = append(priorities, it)
		}
	}
	if it := longest(priorities); it != nil {
		return fromItem(it, "One of today's priorities")
	}
	type candidate struct {
		goal Goal
		step Step
	}
	var steps []candidate
	for _, g := range goals {
		if g.ArchivedAt != nil || g.NextStep == nil || g.NextStep.Minutes > minutes {
			continue
		}
		steps = append(steps, candidate{g, *g.NextStep})
	}
	if len(steps) > 0 {
		sort.SliceStable(steps, func(i, j int) bool {
			if steps[i].step.Minutes != steps[j].step.Minutes {
				return steps[i].step.Minutes > steps[j].step.Minutes
			}
			return steps[i].step.ID < steps[j].step.ID
		})
		c := steps[0]
		return &Suggestion{Title: c.step.Title, Minutes: c.step.Minutes, Reason: fmt.Sprintf("Next step toward %q", c.goal.Title), Source: "goal_step", GoalID: c.goal.ID, StepID: c.step.ID}
	}
	if it := longest(open); it != nil {
		return fromItem(it, "Fits your time and is already on today's list")
	}
	return nil
}

type Replan struct {
	Keep           []PlanItem `json:"keep"`
	MoveToTomorrow []PlanItem `json:"move_to_tomorrow"`
	MinutesKept    int        `json:"minutes_kept" jsonschema:"minutes of open work that fit"`
}

// replan keeps what is fixed (events, finished work), then fills the time left with priorities first and shorter work
// first within a tier. What does not fit moves to tomorrow instead of being dropped.
func replan(items []PlanItem, minutesLeft int) Replan {
	r := Replan{Keep: []PlanItem{}, MoveToTomorrow: []PlanItem{}}
	var open []PlanItem
	for _, it := range items {
		if it.DeletedAt != nil {
			continue
		}
		if it.Kind == "event" || it.DoneAt != nil {
			r.Keep = append(r.Keep, it)
		} else {
			open = append(open, it)
		}
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].IsPriority != open[j].IsPriority {
			return open[i].IsPriority
		}
		if open[i].Minutes != open[j].Minutes {
			return open[i].Minutes < open[j].Minutes
		}
		return open[i].ID < open[j].ID
	})
	budget := minutesLeft
	for _, it := range open {
		if it.Minutes <= budget {
			r.Keep = append(r.Keep, it)
			budget -= it.Minutes
			r.MinutesKept += it.Minutes
		} else {
			r.MoveToTomorrow = append(r.MoveToTomorrow, it)
		}
	}
	return r
}

// dayClose summarises a day and decides what carries over.
type DayClose struct {
	Done         int        `json:"done"`
	Planned      int        `json:"planned" jsonschema:"non-event items on the day, finished or not"`
	Carried      []PlanItem `json:"carried" jsonschema:"unfinished items moved to the next day"`
	Summary      string     `json:"summary"`
	TomorrowNote string     `json:"tomorrow_note"`
}

func closeDay(items []PlanItem) DayClose {
	c := DayClose{Carried: []PlanItem{}}
	for _, it := range items {
		if it.DeletedAt != nil || it.Kind == "event" {
			continue
		}
		c.Planned++
		if it.DoneAt != nil {
			c.Done++
		} else {
			c.Carried = append(c.Carried, it)
		}
	}
	n := len(c.Carried)
	switch {
	case c.Planned == 0:
		c.Summary = "A quiet day. Nothing was planned."
	case n == 0:
		c.Summary = fmt.Sprintf("You finished all %d. Well done.", c.Done)
	case c.Done == 0:
		c.Summary = "Nothing got done today, and that's allowed. Tomorrow starts clean."
	default:
		c.Summary = fmt.Sprintf("You finished %d and %d carry over.", c.Done, n)
	}
	switch {
	case n > 3:
		c.TomorrowNote = "Tomorrow, choose only your top three. The rest can wait."
	case n > 0 && hasPriority(c.Carried) != "":
		c.TomorrowNote = fmt.Sprintf("Start tomorrow with %q.", hasPriority(c.Carried))
	case c.Done > 0:
		c.TomorrowNote = "Repeat what worked today."
	default:
		c.TomorrowNote = "Pick one small thing to start with."
	}
	return c
}

func hasPriority(items []PlanItem) string {
	for _, it := range items {
		if it.IsPriority {
			return it.Title
		}
	}
	return ""
}

// streak counts consecutive scheduled days with a check-in, ending at `day` (or the day before, while today is still open).
// Days the routine is not scheduled neither extend nor break the streak.
func streak(day dayNumber, scheduled func(dayNumber) bool, checked map[dayNumber]bool) int {
	n := 0
	for d := day; d > day-400; d-- {
		if !scheduled(d) {
			continue
		}
		if checked[d] {
			n++
		} else if d != day {
			break
		}
	}
	return n
}

// dayNumber is a calendar day as days since 1970-01-01, so day arithmetic never meets a time zone.
type dayNumber int

func (d dayNumber) String() string { return fromDayNumber(d) }
