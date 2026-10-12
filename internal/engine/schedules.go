package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Instruction string     `json:"instruction"`
	TimeOfDay   string     `json:"time_of_day"`
	TimeZone    string     `json:"time_zone"`
	Recurrence  Recurrence `json:"recurrence"`
	Enabled     bool       `json:"enabled"`
	NextAt      time.Time  `json:"next_at"`
	LastFiredAt *time.Time `json:"last_fired_at"`
	Version     int64      `json:"version"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
type ScheduleData struct {
	Name        string     `json:"name" jsonschema:"short unique name, for example Weekly review"`
	Instruction string     `json:"instruction" jsonschema:"what the person wants the agent to do each time, in their words"`
	TimeOfDay   string     `json:"time_of_day" jsonschema:"HH:MM local time"`
	TimeZone    string     `json:"time_zone,omitempty" jsonschema:"IANA zone; defaults to the person's setting"`
	Recurrence  Recurrence `json:"recurrence" jsonschema:"how often; schedules always repeat (use a reminder for a one-off)"`
	Enabled     *bool      `json:"enabled,omitempty" jsonschema:"default true; a disabled schedule never fires"`
}
type CreateScheduleInput struct {
	Meta
	ID string `json:"id,omitempty"`
	ScheduleData
}
type UpdateScheduleInput struct {
	EditInput
	ScheduleData
}
type ListSchedulesInput struct {
	ListInput
}

const scheduleCols = `id::text,name,instruction,time_of_day,time_zone,recurrence,enabled,next_at,last_fired_at,version,deleted_at,created_at,updated_at`

func scanSchedule(row scanner) (Schedule, error) {
	var s Schedule
	var rec []byte
	if err := row.Scan(&s.ID, &s.Name, &s.Instruction, &s.TimeOfDay, &s.TimeZone, &rec, &s.Enabled, &s.NextAt, &s.LastFiredAt, &s.Version, &s.DeletedAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return s, err
	}
	for _, t := range []*time.Time{s.LastFiredAt, s.DeletedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	s.NextAt, s.CreatedAt, s.UpdatedAt = s.NextAt.UTC(), s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return s, json.Unmarshal(rec, &s.Recurrence)
}

func (u *unit) getSchedule(ctx context.Context, key string, forUpdate bool) (Schedule, error) {
	k, e := id(key)
	if e != nil {
		return Schedule{}, e
	}
	q := "SELECT " + scheduleCols + " FROM life.schedules WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanSchedule(u.tx.QueryRow(ctx, q, k))
}

type scheduleValues struct {
	name, instruction, tod, tz string
	rec                        Recurrence
	enabled                    bool
	next                       time.Time
}

func (u *unit) scheduleValues(ctx context.Context, d ScheduleData) (scheduleValues, error) {
	v := scheduleValues{name: strings.TrimSpace(d.Name), instruction: strings.TrimSpace(d.Instruction), tod: d.TimeOfDay, tz: d.TimeZone, rec: d.Recurrence, enabled: d.Enabled == nil || *d.Enabled}
	if v.name == "" || len(v.name) > 80 {
		return v, invalid("name must be 1-80 characters")
	}
	if v.instruction == "" || len(v.instruction) > 2000 {
		return v, invalid("instruction must be 1-2000 characters")
	}
	if e := validClock("time_of_day", v.tod); e != nil {
		return v, e
	}
	if v.tz == "" {
		s, e := u.getSettings(ctx)
		if e != nil {
			return v, e
		}
		v.tz = s.TimeZone
	}
	loc, e := zone(v.tz)
	if e != nil {
		return v, e
	}
	if e = v.rec.normalize(); e != nil {
		return v, e
	}
	now, e := u.now(ctx)
	if e != nil {
		return v, e
	}
	v.next, e = scheduleNext(now, v.tod, loc, v.rec)
	return v, e
}

func (u *unit) nameFree(ctx context.Context, name, exceptID string) error {
	var n int
	if e := u.tx.QueryRow(ctx, `SELECT count(*) FROM life.schedules WHERE lower(name)=lower($1) AND deleted_at IS NULL AND id::text<>$2`, name, exceptID).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return conflict("A schedule with that name already exists")
	}
	return nil
}

func createSchedule(ctx context.Context, u *unit, in CreateScheduleInput) (Schedule, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Schedule{}, e
	}
	v, e := u.scheduleValues(ctx, in.ScheduleData)
	if e != nil {
		return Schedule{}, e
	}
	if e = u.nameFree(ctx, v.name, key); e != nil {
		return Schedule{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.schedules(id,name,instruction,time_of_day,time_zone,recurrence,enabled,next_at,session_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`,
		key, v.name, v.instruction, v.tod, v.tz, jsonOrNil(&v.rec), v.enabled, v.next, u.session); e != nil {
		return Schedule{}, e
	}
	s, e := u.getSchedule(ctx, key, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "schedule", key, nil, s)
}

func updateSchedule(ctx context.Context, u *unit, in UpdateScheduleInput) (Schedule, error) {
	old, e := u.getSchedule(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the schedule before editing it")
	}
	v, e := u.scheduleValues(ctx, in.ScheduleData)
	if e != nil {
		return old, e
	}
	if e = u.nameFree(ctx, v.name, old.ID); e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.schedules SET name=$2,instruction=$3,time_of_day=$4,time_zone=$5,recurrence=$6,enabled=$7,next_at=$8,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.name, v.instruction, v.tod, v.tz, jsonOrNil(&v.rec), v.enabled, v.next); e != nil {
		return old, e
	}
	return u.scheduleChanged(ctx, old)
}

func (u *unit) scheduleChanged(ctx context.Context, old Schedule) (Schedule, error) {
	s, e := u.getSchedule(ctx, old.ID, false)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "schedule", s.ID, old, s)
}

func lifecycleSchedule(ctx context.Context, u *unit, in EditInput, restore bool) (Schedule, error) {
	old, e := u.getSchedule(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Schedule is not deleted")
		}
		return old, conflict("Schedule is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		if e = u.nameFree(ctx, old.Name, old.ID); e != nil {
			return old, e
		}
		// A restored schedule resumes from now, not from the occurrences it missed while deleted.
		loc, _ := zone(old.TimeZone)
		now, e := u.now(ctx)
		if e != nil {
			return old, e
		}
		next, e := scheduleNext(now, old.TimeOfDay, loc, old.Recurrence)
		if e != nil {
			return old, e
		}
		if _, e = u.tx.Exec(ctx, `UPDATE life.schedules SET next_at=$2 WHERE id=$1`, old.ID, next); e != nil {
			return old, e
		}
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.schedules SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.scheduleChanged(ctx, old)
}

func listSchedules(ctx context.Context, u *unit, in ListSchedulesInput) (Page[Schedule], error) {
	result := Page[Schedule]{Items: []Schedule{}}
	limit, offset, state, e := paging(in.ListInput)
	if e != nil {
		return result, e
	}
	where, args := []string{"true"}, []any{}
	switch state {
	case "active":
		where = append(where, "deleted_at IS NULL")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	}
	if in.Search != "" {
		args = append(args, like(in.Search))
		where = append(where, "(name ILIKE $1 OR instruction ILIKE $1)")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.schedules WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	args = append(args, limit, offset)
	rows, e := u.tx.Query(ctx, "SELECT "+scheduleCols+" FROM life.schedules WHERE "+clause+" ORDER BY next_at, id LIMIT $"+strconv.Itoa(len(args)-1)+" OFFSET $"+strconv.Itoa(len(args)), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		s, e := scanSchedule(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, s)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}
