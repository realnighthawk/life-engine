package engine

import (
	"context"
	"fmt"
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
	RemindAt     *string    `json:"remind_at" jsonschema:"HH:MM local time the person wants a nudge, or null"`
	TimeZone     string     `json:"time_zone"`
	Version      int64      `json:"version"`
	DeletedAt    *time.Time `json:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
type RoutineData struct {
	Title        string  `json:"title" jsonschema:"the usual version, 1-200 characters"`
	Minutes      int     `json:"minutes,omitempty" jsonschema:"1-240, default 15"`
	ShortTitle   string  `json:"short_title,omitempty" jsonschema:"the busy-day version; default 'A smaller version'"`
	ShortMinutes int     `json:"short_minutes,omitempty" jsonschema:"1-240, default 5"`
	Weekdays     []int   `json:"weekdays,omitempty" jsonschema:"0 = Sunday to 6; omit for every day"`
	RemindAt     *string `json:"remind_at,omitempty" jsonschema:"HH:MM local time for a nudge if it is not done yet"`
	TimeZone     string  `json:"time_zone,omitempty" jsonschema:"IANA zone; defaults to the person's setting"`
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
	Done          bool    `json:"done"`
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

const routineCols = `id::text,title,minutes,short_title,short_minutes,weekdays,remind_at,time_zone,version,deleted_at,created_at,updated_at`

func scanRoutine(row scanner) (Routine, error) {
	var r Routine
	var wd []int16
	err := row.Scan(&r.ID, &r.Title, &r.Minutes, &r.ShortTitle, &r.ShortMinutes, &wd, &r.RemindAt, &r.TimeZone, &r.Version, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt)
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
	remind            *string
}

func (u *unit) routineValues(ctx context.Context, d RoutineData) (routineValues, error) {
	v := routineValues{title: strings.TrimSpace(d.Title), short: strings.TrimSpace(d.ShortTitle), minutes: d.Minutes, shortMin: d.ShortMinutes, tz: d.TimeZone, remind: d.RemindAt}
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
	if v.remind != nil {
		if e := validClock("remind_at", *v.remind); e != nil {
			return v, e
		}
	}
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
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.routines(id,title,minutes,short_title,short_minutes,weekdays,remind_at,time_zone,session_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`,
		key, v.title, v.minutes, v.short, v.shortMin, v.weekdays, v.remind, v.tz, u.session); e != nil {
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
	if _, e = u.tx.Exec(ctx, `UPDATE life.routines SET title=$2,minutes=$3,short_title=$4,short_minutes=$5,weekdays=$6,remind_at=$7,time_zone=$8,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.title, v.minutes, v.short, v.shortMin, v.weekdays, v.remind, v.tz); e != nil {
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

// checkins returns the days (as numbers) a routine was done in the last 400 days up to d, with the variant used on d.
func (u *unit) checkins(ctx context.Context, routineID string, d string) (map[dayNumber]bool, map[dayNumber]string, error) {
	rows, e := u.tx.Query(ctx, `SELECT day::text,variant FROM life.routine_checkins WHERE routine_id=$1 AND day<=$2 AND day>$2::date-400`, routineID, d)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	done, variant := map[dayNumber]bool{}, map[dayNumber]string{}
	for rows.Next() {
		var ds, v string
		if e = rows.Scan(&ds, &v); e != nil {
			return nil, nil, e
		}
		n := toDayNumber(ds)
		done[n], variant[n] = true, v
	}
	return done, variant, rows.Err()
}

func (u *unit) routineStatus(ctx context.Context, r Routine, d string) (RoutineStatus, error) {
	done, variant, e := u.checkins(ctx, r.ID, d)
	if e != nil {
		return RoutineStatus{}, e
	}
	today := toDayNumber(d)
	s := RoutineStatus{Routine: r, Scheduled: r.scheduledOn(today), Done: done[today], Variant: variant[today]}
	s.Streak = streak(today, r.scheduledOn, done)
	last, haveLast := dayNumber(0), false
	for n := range done {
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
	today, _, _, e := u.today(ctx)
	if e != nil {
		return RoutineStatus{}, e
	}
	d := in.Day
	if d == "" {
		d = today
	} else if d, e = day("day", d); e != nil {
		return RoutineStatus{}, e
	}
	if d > today {
		return RoutineStatus{}, invalid("day cannot be in the future")
	}
	var before string
	_ = u.tx.QueryRow(ctx, `SELECT variant FROM life.routine_checkins WHERE routine_id=$1 AND day=$2`, r.ID, d).Scan(&before)
	if in.Variant == "none" {
		_, e = u.tx.Exec(ctx, `DELETE FROM life.routine_checkins WHERE routine_id=$1 AND day=$2`, r.ID, d)
	} else {
		_, e = u.tx.Exec(ctx, `INSERT INTO life.routine_checkins(routine_id,day,variant) VALUES($1,$2,$3)
			ON CONFLICT (routine_id,day) DO UPDATE SET variant=EXCLUDED.variant, at=now()`, r.ID, d, in.Variant)
	}
	if e != nil {
		return RoutineStatus{}, e
	}
	if before != in.Variant && !(before == "" && in.Variant == "none") {
		if e = u.audit(ctx, "routine", r.ID, map[string]string{"day": d, "variant": before}, map[string]string{"day": d, "variant": in.Variant}); e != nil {
			return RoutineStatus{}, e
		}
	}
	return u.routineStatus(ctx, r, today)
}
