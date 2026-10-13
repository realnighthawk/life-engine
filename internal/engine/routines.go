package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Routine struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Minutes      int        `json:"minutes"`
	ShortTitle   string     `json:"short_title" jsonschema:"the smaller version for a busy day"`
	ShortMinutes int        `json:"short_minutes"`
	Weekdays     []int      `json:"weekdays" jsonschema:"days it applies, 0 = Sunday to 6; empty means every day"`
	TargetPerDay int        `json:"target_per_day" jsonschema:"times to do it on each day it applies; the day is done when the count reaches this"`
	Cues         []string   `json:"cues" jsonschema:"HH:MM local times the person is nudged; each nudge checks the day's count first"`
	TimeZone     string     `json:"time_zone"`
	Version      int64      `json:"version"`
	DeletedAt    *time.Time `json:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
type RoutineData struct {
	Title        string   `json:"title" jsonschema:"the usual version, 1-200 characters"`
	Minutes      int      `json:"minutes,omitempty" jsonschema:"1-240, default 15"`
	ShortTitle   string   `json:"short_title,omitempty" jsonschema:"the busy-day version; default 'A smaller version'"`
	ShortMinutes int      `json:"short_minutes,omitempty" jsonschema:"1-240, default 5"`
	Weekdays     []int    `json:"weekdays,omitempty" jsonschema:"0 = Sunday to 6; omit for every day"`
	TargetPerDay *int     `json:"target_per_day,omitempty" jsonschema:"times to do it each day it applies, 1-12, default 1 (3 for drink water)"`
	Cues         []string `json:"cues,omitempty" jsonschema:"HH:MM local times to be nudged, up to 8, for example [\"09:00\",\"13:00\",\"17:00\"]; a nudge only speaks if the day is not yet done"`
	TimeZone     string   `json:"time_zone,omitempty" jsonschema:"IANA zone; defaults to the person's setting"`
}
type CreateRoutineInput struct {
	Meta
	ID string `json:"id,omitempty"`
	RoutineData
}
type UpdateRoutineInput struct {
	EditInput
	RoutineData
}
type CheckinInput struct {
	Meta
	RoutineID string `json:"routine_id"`
	Day       string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD, today or earlier; default today"`
	Variant   string `json:"variant" jsonschema:"full or short counts it done; none clears it"`
}
type RoutinesStatusInput struct {
	Day string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
}
type RoutineStatus struct {
	Routine       Routine `json:"routine"`
	Scheduled     bool    `json:"scheduled" jsonschema:"whether the routine applies on this day"`
	Target        int     `json:"target" jsonschema:"times to do it on this day"`
	Count         int     `json:"count" jsonschema:"times done on this day so far"`
	Done          bool    `json:"done" jsonschema:"count has reached the target"`
	Variant       string  `json:"variant,omitempty"`
	Streak        int     `json:"streak" jsonschema:"consecutive scheduled days done; days it does not apply neither extend nor break it"`
	DaysSinceLast *int    `json:"days_since_last" jsonschema:"days since the last check-in, or null if never"`
	Gap           bool    `json:"gap" jsonschema:"three or more days without a check-in"`
	Hint          string  `json:"hint"`
}
type RoutinesStatus struct {
	Day   string          `json:"day"`
	Items []RoutineStatus `json:"items"`
}

const routineCols = `id::text,title,minutes,short_title,short_minutes,weekdays,target_per_day,cues,time_zone,version,deleted_at,created_at,updated_at`

func scanRoutine(row scanner) (Routine, error) {
	var r Routine
	var wd []int16
	err := row.Scan(&r.ID, &r.Title, &r.Minutes, &r.ShortTitle, &r.ShortMinutes, &wd, &r.TargetPerDay, &r.Cues, &r.TimeZone, &r.Version, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt)
	if r.Cues == nil {
		r.Cues = []string{}
	}
	r.Weekdays = make([]int, len(wd))
	for i, d := range wd {
		r.Weekdays[i] = int(d)
	}
	utcPtr(r.DeletedAt)
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return r, err
}

func (u *unit) getRoutine(ctx context.Context, key string, forUpdate bool) (Routine, error) {
	k, e := id(key)
	if e != nil {
		return Routine{}, e
	}
	q := "SELECT " + routineCols + " FROM life.routines WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanRoutine(u.tx.QueryRow(ctx, q, k))
}

type routineValues struct {
	title, short, tz  string
	minutes, shortMin int
	weekdays          []int16
	target            int
	cues              []string
}

func (u *unit) routineValues(ctx context.Context, d RoutineData) (routineValues, error) {
	v := routineValues{title: strings.TrimSpace(d.Title), short: strings.TrimSpace(d.ShortTitle), minutes: d.Minutes, shortMin: d.ShortMinutes, tz: d.TimeZone, target: 1}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if v.short == "" {
		v.short = "A smaller version"
	}
	if len(v.short) > 200 {
		return v, invalid("short_title must be at most 200 characters")
	}
	if v.minutes == 0 {
		v.minutes = 15
	}
	if v.shortMin == 0 {
		v.shortMin = 5
	}
	if v.minutes < 1 || v.minutes > 240 || v.shortMin < 1 || v.shortMin > 240 {
		return v, invalid("minutes must be 1-240")
	}
	seen := map[int]bool{}
	for _, w := range d.Weekdays {
		if w < 0 || w > 6 || seen[w] {
			return v, invalid("weekdays must be unique days 0-6")
		}
		seen[w] = true
		v.weekdays = append(v.weekdays, int16(w))
	}
	if v.weekdays == nil {
		v.weekdays = []int16{}
	}
	if d.TargetPerDay != nil {
		v.target = *d.TargetPerDay
	}
	if v.target < 1 || v.target > 12 {
		return v, invalid("target_per_day must be 1-12")
	}
	if len(d.Cues) > 8 {
		return v, invalid("at most 8 cues")
	}
	v.cues = []string{}
	for _, c := range d.Cues {
		if e := validClock("cues", c); e != nil {
			return v, e
		}
		if slices.Contains(v.cues, c) {
			return v, invalid("cues must be different times")
		}
		v.cues = append(v.cues, c)
	}
	slices.Sort(v.cues)
	if v.tz == "" {
		s, e := u.getSettings(ctx)
		if e != nil {
			return v, e
		}
		v.tz = s.TimeZone
	}
	if _, e := zone(v.tz); e != nil {
		return v, e
	}
	return v, nil
}

func createRoutine(ctx context.Context, u *unit, in CreateRoutineInput) (Routine, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Routine{}, e
	}
	v, e := u.routineValues(ctx, in.RoutineData)
	if e != nil {
		return Routine{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.routines(id,title,minutes,short_title,short_minutes,weekdays,target_per_day,cues,time_zone,session_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''))`,
		key, v.title, v.minutes, v.short, v.shortMin, v.weekdays, v.target, v.cues, v.tz, u.session); e != nil {
		return Routine{}, e
	}
	r, e := u.getRoutine(ctx, key, false)
	if e != nil {
		return r, e
	}
	return r, u.audit(ctx, "routine", key, nil, r)
}

func (u *unit) routineChanged(ctx context.Context, old Routine) (Routine, error) {
	r, e := u.getRoutine(ctx, old.ID, false)
	if e != nil {
		return r, e
	}
	return r, u.audit(ctx, "routine", r.ID, old, r)
}

func updateRoutine(ctx context.Context, u *unit, in UpdateRoutineInput) (Routine, error) {
	old, e := u.getRoutine(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the routine before editing it")
	}
	v, e := u.routineValues(ctx, in.RoutineData)
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.routines SET title=$2,minutes=$3,short_title=$4,short_minutes=$5,weekdays=$6,target_per_day=$7,cues=$8,time_zone=$9,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.title, v.minutes, v.short, v.shortMin, v.weekdays, v.target, v.cues, v.tz); e != nil {
		return old, e
	}
	return u.routineChanged(ctx, old)
}

func lifecycleRoutine(ctx context.Context, u *unit, in EditInput, restore bool) (Routine, error) {
	old, e := u.getRoutine(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Routine is not deleted")
		}
		return old, conflict("Routine is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.routines SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.routineChanged(ctx, old)
}

func listRoutines(ctx context.Context, u *unit, in ListInput) (Page[Routine], error) {
	result := Page[Routine]{Items: []Routine{}}
	limit, offset, state, e := paging(in)
	if e != nil {
		return result, e
	}
	where, args := "true", []any{}
	switch state {
	case "active":
		where = "deleted_at IS NULL"
	case "deleted":
		where = "deleted_at IS NOT NULL"
	}
	if in.Search != "" {
		args = append(args, like(in.Search))
		where += " AND title ILIKE $1"
	}
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.routines WHERE "+where, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	args = append(args, limit, offset)
	rows, e := u.tx.Query(ctx, "SELECT "+routineCols+" FROM life.routines WHERE "+where+" ORDER BY created_at, id LIMIT $"+itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scanRoutine(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, r)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}

func (r Routine) scheduledOn(d dayNumber) bool {
	if len(r.Weekdays) == 0 {
		return true
	}
	for _, w := range r.Weekdays {
		if w == weekdayOf(d) {
			return true
		}
	}
	return false
}

type dayLog struct {
	count   int
	variant string
}

// checkins returns what was logged on each day in the last 400 days up to d.
func (u *unit) checkins(ctx context.Context, routineID string, d string) (map[dayNumber]dayLog, error) {
	rows, e := u.tx.Query(ctx, `SELECT day::text,variant,count FROM life.routine_checkins WHERE routine_id=$1 AND day<=$2 AND day>$2::date-400`, routineID, d)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	logs := map[dayNumber]dayLog{}
	for rows.Next() {
		var ds, v string
		var n int
		if e = rows.Scan(&ds, &v, &n); e != nil {
			return nil, e
		}
		logs[toDayNumber(ds)] = dayLog{n, v}
	}
	return logs, rows.Err()
}

// doneDays are the days whose count reached the routine's target.
func doneDays(logs map[dayNumber]dayLog, target int) map[dayNumber]bool {
	done := map[dayNumber]bool{}
	for n, l := range logs {
		done[n] = l.count >= target
	}
	return done
}

func (u *unit) routineStatus(ctx context.Context, r Routine, d string) (RoutineStatus, error) {
	logs, e := u.checkins(ctx, r.ID, d)
	if e != nil {
		return RoutineStatus{}, e
	}
	done := doneDays(logs, r.TargetPerDay)
	today := toDayNumber(d)
	s := RoutineStatus{Routine: r, Target: r.TargetPerDay, Count: logs[today].count, Scheduled: r.scheduledOn(today), Done: done[today], Variant: logs[today].variant}
	s.Streak = streak(today, r.scheduledOn, done)
	last, haveLast := dayNumber(0), false
	for n := range logs { // any activity, even a partial day, counts as showing up
		if !haveLast || n > last {
			last, haveLast = n, true
		}
	}
	if haveLast {
		days := int(today - last)
		s.DaysSinceLast = &days
		s.Gap = days >= 3
	} else if created := int(today - toDayNumber(r.CreatedAt.Format("2006-01-02"))); created >= 3 {
		s.Gap = true
	}
	switch {
	case s.Done:
		s.Hint = "Done today."
	case s.Gap && s.DaysSinceLast != nil:
		s.Hint = fmt.Sprintf("It's been %d days. Start again with the short version: %s (%d min).", *s.DaysSinceLast, r.ShortTitle, r.ShortMinutes)
	case s.Gap:
		s.Hint = fmt.Sprintf("Not started yet. Begin with the short version: %s (%d min).", r.ShortTitle, r.ShortMinutes)
	case s.Scheduled && s.Count > 0:
		s.Hint = fmt.Sprintf("%d of %d today so far.", s.Count, s.Target)
	case s.Scheduled:
		s.Hint = fmt.Sprintf("Today: %s (%d min), or the short version: %s (%d min).", r.Title, r.Minutes, r.ShortTitle, r.ShortMinutes)
	default:
		s.Hint = "Not scheduled today."
	}
	return s, nil
}

func routinesStatus(ctx context.Context, u *unit, in RoutinesStatusInput) (RoutinesStatus, error) {
	d, e := u.dayOrToday(ctx, in.Day)
	out := RoutinesStatus{Day: d, Items: []RoutineStatus{}}
	if e != nil {
		return out, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+routineCols+" FROM life.routines WHERE deleted_at IS NULL ORDER BY created_at, id")
	if e != nil {
		return out, e
	}
	var routines []Routine
	for rows.Next() {
		r, e := scanRoutine(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		routines = append(routines, r)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	for _, r := range routines {
		s, e := u.routineStatus(ctx, r, d)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, s)
	}
	return out, nil
}

func (u *unit) logDay(ctx context.Context, requested string) (today, d string, e error) {
	today, _, _, e = u.today(ctx)
	if e != nil {
		return
	}
	d = requested
	if d == "" {
		d = today
	} else if d, e = day("day", d); e != nil {
		return
	}
	if d > today {
		e = invalid("day cannot be in the future")
	}
	return
}

// setCheckin marks the whole day done (the count reaches the target) in the full or short version, or clears the day.
func setCheckin(ctx context.Context, u *unit, in CheckinInput) (RoutineStatus, error) {
	r, e := u.getRoutine(ctx, in.RoutineID, true)
	if e != nil {
		return RoutineStatus{}, e
	}
	if r.DeletedAt != nil {
		return RoutineStatus{}, conflict("Restore the routine before checking in")
	}
	if !oneOf(in.Variant, "full", "short", "none") {
		return RoutineStatus{}, invalid("variant must be full, short or none")
	}
	today, d, e := u.logDay(ctx, in.Day)
	if e != nil {
		return RoutineStatus{}, e
	}
	count := r.TargetPerDay
	if in.Variant == "none" {
		count = 0
	}
	if e = u.setCount(ctx, r, d, count, in.Variant); e != nil {
		return RoutineStatus{}, e
	}
	return u.routineStatus(ctx, r, today)
}

// setCount writes a day's count (0 clears it), auditing a change.
func (u *unit) setCount(ctx context.Context, r Routine, d string, count int, variant string) error {
	var before int
	var beforeVariant string
	_ = u.tx.QueryRow(ctx, `SELECT count,variant FROM life.routine_checkins WHERE routine_id=$1 AND day=$2`, r.ID, d).Scan(&before, &beforeVariant)
	var e error
	if count == 0 {
		_, e = u.tx.Exec(ctx, `DELETE FROM life.routine_checkins WHERE routine_id=$1 AND day=$2`, r.ID, d)
	} else {
		_, e = u.tx.Exec(ctx, `INSERT INTO life.routine_checkins(routine_id,day,variant,count) VALUES($1,$2,$3,$4)
			ON CONFLICT (routine_id,day) DO UPDATE SET variant=EXCLUDED.variant, count=EXCLUDED.count, at=now()`, r.ID, d, variant, count)
	}
	if e != nil {
		return e
	}
	if before != count || (count > 0 && beforeVariant != variant) {
		return u.audit(ctx, "routine", r.ID, map[string]any{"day": d, "count": before, "variant": beforeVariant}, map[string]any{"day": d, "count": count, "variant": variant})
	}
	return nil
}

type LogInput struct {
	Meta
	RoutineID string `json:"routine_id"`
	Day       string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD, today or earlier; default today"`
	Delta     *int   `json:"delta,omitempty" jsonschema:"change to the day's count: 1 (default) records one more time, -1 takes one back"`
	Variant   string `json:"variant,omitempty" jsonschema:"full (default) or short, for the time(s) being recorded"`
}

// logRoutine records one more time (or takes one back). The day is done when the count reaches the routine's target.
func logRoutine(ctx context.Context, u *unit, in LogInput) (RoutineStatus, error) {
	r, e := u.getRoutine(ctx, in.RoutineID, true)
	if e != nil {
		return RoutineStatus{}, e
	}
	if r.DeletedAt != nil {
		return RoutineStatus{}, conflict("Restore the routine before logging it")
	}
	variant := in.Variant
	if variant == "" {
		variant = "full"
	}
	if !oneOf(variant, "full", "short") {
		return RoutineStatus{}, invalid("variant must be full or short")
	}
	delta := 1
	if in.Delta != nil {
		delta = *in.Delta
	}
	if delta < -12 || delta > 12 || delta == 0 {
		return RoutineStatus{}, invalid("delta must be a non-zero number between -12 and 12")
	}
	today, d, e := u.logDay(ctx, in.Day)
	if e != nil {
		return RoutineStatus{}, e
	}
	var before int
	_ = u.tx.QueryRow(ctx, `SELECT count FROM life.routine_checkins WHERE routine_id=$1 AND day=$2`, r.ID, d).Scan(&before)
	after := min(max(before+delta, 0), 50)
	if e = u.setCount(ctx, r, d, after, variant); e != nil {
		return RoutineStatus{}, e
	}
	return u.routineStatus(ctx, r, today)
}

type HistoryDay struct {
	Day       string `json:"day"`
	Scheduled bool   `json:"scheduled"`
	Count     int    `json:"count"`
	Target    int    `json:"target"`
	Done      bool   `json:"done"`
}
type Consistency struct {
	Done      int `json:"done" jsonschema:"scheduled days whose count reached the target"`
	Scheduled int `json:"scheduled" jsonschema:"days in the window the routine applied"`
}
type RoutineHistoryInput struct {
	RoutineID string `json:"routine_id"`
	Days      int    `json:"days,omitempty" jsonschema:"how many days back to include, 7-120, default 30"`
}

// RoutineHistory is what a tracker screen draws: the recent days, how consistent the person has been, and their best run.
// Consistency is a rate over the window, never a streak that a missed day resets.
type RoutineHistory struct {
	Routine    Routine      `json:"routine"`
	Days       []HistoryDay `json:"days" jsonschema:"oldest first, ending today"`
	Last7      Consistency  `json:"last_7"`
	Last30     Consistency  `json:"last_30"`
	Streak     int          `json:"streak"`
	BestStreak int          `json:"best_streak" jsonschema:"longest run of scheduled days done in the last 400 days"`
}

func routineHistory(ctx context.Context, u *unit, in RoutineHistoryInput) (RoutineHistory, error) {
	r, e := u.getRoutine(ctx, in.RoutineID, false)
	if e != nil {
		return RoutineHistory{}, e
	}
	days := in.Days
	if days == 0 {
		days = 30
	}
	if days < 7 || days > 120 {
		return RoutineHistory{}, invalid("days must be 7-120")
	}
	today, _, _, e := u.today(ctx)
	if e != nil {
		return RoutineHistory{}, e
	}
	logs, e := u.checkins(ctx, r.ID, today)
	if e != nil {
		return RoutineHistory{}, e
	}
	done := doneDays(logs, r.TargetPerDay)
	now := toDayNumber(today)
	out := RoutineHistory{Routine: r, Days: make([]HistoryDay, 0, days), Streak: streak(now, r.scheduledOn, done)}
	for i := days - 1; i >= 0; i-- {
		n := now - dayNumber(i)
		h := HistoryDay{Day: fromDayNumber(n), Scheduled: r.scheduledOn(n), Count: logs[n].count, Target: r.TargetPerDay, Done: done[n]}
		out.Days = append(out.Days, h)
		if h.Scheduled {
			if i < 7 {
				out.Last7.Scheduled++
				if h.Done {
					out.Last7.Done++
				}
			}
			if i < 30 {
				out.Last30.Scheduled++
				if h.Done {
					out.Last30.Done++
				}
			}
		}
	}
	run := 0
	for n := now - 399; n <= now; n++ {
		if !r.scheduledOn(n) {
			continue
		}
		if done[n] {
			run++
			out.BestStreak = max(out.BestStreak, run)
		} else if n != now { // today may still be done
			run = 0
		}
	}
	return out, nil
}
