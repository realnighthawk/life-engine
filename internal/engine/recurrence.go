package engine

import "time"

// Recurrence repeats an item on local wall-clock time in its own zone, so "9:00 every weekday" stays 9:00 across daylight saving.
type Recurrence struct {
	Freq     string `json:"freq" jsonschema:"daily, weekly or monthly"`
	Interval int    `json:"interval,omitempty" jsonschema:"repeat every N days/weeks/months, 1-365, default 1"`
	Weekdays []int  `json:"weekdays,omitempty" jsonschema:"weekly only: days of the week, 0 = Sunday to 6 = Saturday; empty repeats on the due date's weekday"`
}

func (r *Recurrence) normalize() error {
	if !oneOf(r.Freq, "daily", "weekly", "monthly") {
		return invalid("recurrence.freq must be daily, weekly or monthly")
	}
	if r.Interval == 0 {
		r.Interval = 1
	}
	if r.Interval < 1 || r.Interval > 365 {
		return invalid("recurrence.interval must be 1-365")
	}
	if len(r.Weekdays) > 0 && r.Freq != "weekly" {
		return invalid("recurrence.weekdays applies to weekly recurrence only")
	}
	seen := map[int]bool{}
	for _, d := range r.Weekdays {
		if d < 0 || d > 6 || seen[d] {
			return invalid("recurrence.weekdays must be unique days 0-6")
		}
		seen[d] = true
	}
	return nil
}

// nextOccurrence returns the first occurrence strictly after both `after` and `due`, counted from the series anchored at `due`.
func nextOccurrence(due, after time.Time, r Recurrence, loc *time.Location) time.Time {
	if after.Before(due) {
		after = due
	}
	anchor := due.In(loc)
	interval := r.Interval
	if interval < 1 {
		interval = 1
	}
	switch r.Freq {
	case "daily":
		return stepSeries(anchor, after, func(k int) time.Time { return anchor.AddDate(0, 0, k*interval) }, 24*time.Hour*time.Duration(interval))
	case "monthly":
		return stepSeries(anchor, after, func(k int) time.Time { return addMonthsClamped(anchor, k*interval) }, 28*24*time.Hour*time.Duration(interval))
	}
	if len(r.Weekdays) == 0 {
		return stepSeries(anchor, after, func(k int) time.Time { return anchor.AddDate(0, 0, 7*k*interval) }, 7*24*time.Hour*time.Duration(interval))
	}
	set := map[time.Weekday]bool{}
	for _, d := range r.Weekdays {
		set[time.Weekday(d)] = true
	}
	weekStart := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d-int(t.Weekday()), 0, 0, 0, 0, loc)
	}
	base := weekStart(anchor)
	// Scan local days from the later of the anchor and `after`.
	day := after.In(loc)
	y, m, d := day.Date()
	for i := 0; i < 366*5; i++ {
		cand := time.Date(y, m, d+i, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, loc)
		if !cand.After(after) || !set[cand.Weekday()] {
			continue
		}
		weeks := int(weekStart(cand).Sub(base).Round(24*time.Hour).Hours() / (24 * 7))
		if weeks%interval == 0 {
			return cand
		}
	}
	return anchor.AddDate(1, 0, 0)
}

// stepSeries finds the first series member after `after`, starting near it instead of at k=1 so long gaps stay cheap.
func stepSeries(anchor, after time.Time, at func(int) time.Time, step time.Duration) time.Time {
	k := 1
	if gap := after.Sub(anchor); gap > 2*step {
		k = int(gap/step) - 1
	}
	for {
		if t := at(k); t.After(after) {
			return t
		}
		k++
	}
}

// addMonthsClamped adds months to the anchor's day-of-month, clamping to the month's last day rather than overflowing.
func addMonthsClamped(anchor time.Time, months int) time.Time {
	y, m, d := anchor.Date()
	first := time.Date(y, m+time.Month(months), 1, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, anchor.Location())
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, anchor.Location()).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, anchor.Location())
}

// scheduleNext returns the first occurrence strictly after `now` of a series at a local time of day.
func scheduleNext(now time.Time, timeOfDay string, loc *time.Location, r Recurrence) (time.Time, error) {
	t, err := time.Parse("15:04", timeOfDay)
	if err != nil {
		return time.Time{}, invalid("time_of_day must be HH:MM")
	}
	y, m, d := now.In(loc).Date()
	anchor := time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, loc)
	allowed := true
	if r.Freq == "weekly" && len(r.Weekdays) > 0 {
		allowed = false
		for _, wd := range r.Weekdays {
			if time.Weekday(wd) == anchor.Weekday() {
				allowed = true
			}
		}
	}
	if anchor.After(now) && allowed {
		return anchor.UTC(), nil
	}
	return nextOccurrence(anchor, now, r, loc).UTC(), nil
}

// occurrencesIn expands a repeating item's series between start (inclusive) and end (exclusive).
func occurrencesIn(anchor time.Time, rule Recurrence, loc *time.Location, start, end time.Time) []time.Time {
	var out []time.Time
	if !anchor.Before(start) && anchor.Before(end) {
		out = append(out, anchor)
	}
	after := start.Add(-time.Second)
	if after.Before(anchor) {
		after = anchor
	}
	t := after
	for i := 0; i < 64; i++ { // a day holds a handful of occurrences at most; the bound only guards a malformed rule
		t = nextOccurrence(anchor, t, rule, loc)
		if !t.Before(end) {
			break
		}
		if !t.Before(start) {
			out = append(out, t)
		}
	}
	return out
}
