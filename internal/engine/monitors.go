package engine

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// A monitor is something the person asked the agent to keep an eye on: it is checked on an interval and the person is told
// only when it matters. It fires as a wake (wake.go); each check is recorded with monitors_record.
type Monitor struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Objective       string     `json:"objective" jsonschema:"what to watch and when the person should be told, in their words"`
	EveryMinutes    int        `json:"every_minutes"`
	Enabled         bool       `json:"enabled"`
	NextAt          time.Time  `json:"next_at"`
	LastCheckedAt   *time.Time `json:"last_checked_at"`
	LastResult      string     `json:"last_result" jsonschema:"one line from the most recent check"`
	LastTriggeredAt *time.Time `json:"last_triggered_at" jsonschema:"when a check last decided the person should be told"`
	Version         int64      `json:"version"`
	DeletedAt       *time.Time `json:"deleted_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type MonitorData struct {
	Name         string `json:"name" jsonschema:"short unique name, for example Flight prices, 1-80 characters"`
	Objective    string `json:"objective" jsonschema:"what to watch and when the person should be told, in their words, 1-2000 characters"`
	EveryMinutes int    `json:"every_minutes" jsonschema:"how often to check, 15 to 44640 (31 days)"`
	Enabled      *bool  `json:"enabled,omitempty" jsonschema:"default true; a disabled monitor is never checked"`
}
type CreateMonitorInput struct {
	Meta
	ID string `json:"id,omitempty"`
	MonitorData
}
type UpdateMonitorInput struct {
	EditInput
	MonitorData
}
type ListMonitorsInput struct {
	ListInput
}
type RecordCheckInput struct {
	Meta
	ID        string `json:"id"`
	Result    string `json:"result" jsonschema:"one line on what the check found, up to 1000 characters"`
	Triggered bool   `json:"triggered" jsonschema:"true only if the person should be told; then also call notify_user"`
}

const monitorCols = `id::text,name,objective,every_minutes,enabled,next_at,last_checked_at,last_result,last_triggered_at,version,deleted_at,created_at,updated_at`

func scanMonitor(row scanner) (Monitor, error) {
	var m Monitor
	if err := row.Scan(&m.ID, &m.Name, &m.Objective, &m.EveryMinutes, &m.Enabled, &m.NextAt, &m.LastCheckedAt, &m.LastResult, &m.LastTriggeredAt,
		&m.Version, &m.DeletedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return m, err
	}
	for _, t := range []*time.Time{m.LastCheckedAt, m.LastTriggeredAt, m.DeletedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	m.NextAt, m.CreatedAt, m.UpdatedAt = m.NextAt.UTC(), m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	return m, nil
}

func (u *unit) getMonitor(ctx context.Context, key string, forUpdate bool) (Monitor, error) {
	k, e := id(key)
	if e != nil {
		return Monitor{}, e
	}
	q := "SELECT " + monitorCols + " FROM life.monitors WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanMonitor(u.tx.QueryRow(ctx, q, k))
}

func (d MonitorData) values() (name, objective string, every int, enabled bool, e error) {
	name, objective, every, enabled = strings.TrimSpace(d.Name), strings.TrimSpace(d.Objective), d.EveryMinutes, d.Enabled == nil || *d.Enabled
	switch {
	case name == "" || len(name) > 80:
		e = invalid("name must be 1-80 characters")
	case objective == "" || len(objective) > 2000:
		e = invalid("objective must be 1-2000 characters")
	case every < 15 || every > 44640:
		e = invalid("every_minutes must be between 15 and 44640")
	}
	return
}

func (u *unit) monitorNameFree(ctx context.Context, name, exceptID string) error {
	var n int
	if e := u.tx.QueryRow(ctx, `SELECT count(*) FROM life.monitors WHERE lower(name)=lower($1) AND deleted_at IS NULL AND id::text<>$2`, name, exceptID).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return conflict("A monitor with that name already exists")
	}
	return nil
}

func createMonitor(ctx context.Context, u *unit, in CreateMonitorInput) (Monitor, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Monitor{}, e
	}
	name, objective, every, enabled, e := in.MonitorData.values()
	if e != nil {
		return Monitor{}, e
	}
	if e = u.monitorNameFree(ctx, name, key); e != nil {
		return Monitor{}, e
	}
	now, e := u.now(ctx)
	if e != nil {
		return Monitor{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.monitors(id,name,objective,every_minutes,enabled,next_at,session_id) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''))`,
		key, name, objective, every, enabled, now.Add(time.Duration(every)*time.Minute), u.session); e != nil {
		return Monitor{}, e
	}
	m, e := u.getMonitor(ctx, key, false)
	if e != nil {
		return m, e
	}
	return m, u.audit(ctx, "monitor", key, nil, m)
}

func (u *unit) monitorChanged(ctx context.Context, old Monitor) (Monitor, error) {
	m, e := u.getMonitor(ctx, old.ID, false)
	if e != nil {
		return m, e
	}
	return m, u.audit(ctx, "monitor", m.ID, old, m)
}

func updateMonitor(ctx context.Context, u *unit, in UpdateMonitorInput) (Monitor, error) {
	old, e := u.getMonitor(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the monitor before editing it")
	}
	name, objective, every, enabled, e := in.MonitorData.values()
	if e != nil {
		return old, e
	}
	if e = u.monitorNameFree(ctx, name, old.ID); e != nil {
		return old, e
	}
	next := old.NextAt
	if every != old.EveryMinutes || (enabled && !old.Enabled) { // a new cadence, or switched back on, counts from now
		now, e := u.now(ctx)
		if e != nil {
			return old, e
		}
		next = now.Add(time.Duration(every) * time.Minute)
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.monitors SET name=$2,objective=$3,every_minutes=$4,enabled=$5,next_at=$6,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, name, objective, every, enabled, next); e != nil {
		return old, e
	}
	return u.monitorChanged(ctx, old)
}

func lifecycleMonitor(ctx context.Context, u *unit, in EditInput, restore bool) (Monitor, error) {
	old, e := u.getMonitor(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Monitor is not deleted")
		}
		return old, conflict("Monitor is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		if e = u.monitorNameFree(ctx, old.Name, old.ID); e != nil {
			return old, e
		}
		now, e := u.now(ctx)
		if e != nil {
			return old, e
		}
		// A restored monitor resumes from now, not from the checks it missed while deleted.
		if _, e = u.tx.Exec(ctx, `UPDATE life.monitors SET next_at=$2 WHERE id=$1`, old.ID, now.Add(time.Duration(old.EveryMinutes)*time.Minute)); e != nil {
			return old, e
		}
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.monitors SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.monitorChanged(ctx, old)
}

// recordCheck is what the agent calls after each check. It is not an edit by the person, so it does not bump the version
// (their open editor must not go stale every time a check runs).
func recordCheck(ctx context.Context, u *unit, in RecordCheckInput) (Monitor, error) {
	old, e := u.getMonitor(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Monitor is deleted")
	}
	result := strings.TrimSpace(in.Result)
	if result == "" || len(result) > 1000 {
		return old, invalid("result must be 1-1000 characters")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.monitors SET last_checked_at=now(),last_result=$2,last_triggered_at=CASE WHEN $3 THEN now() ELSE last_triggered_at END,updated_at=now() WHERE id=$1`,
		old.ID, result, in.Triggered); e != nil {
		return old, e
	}
	return u.monitorChanged(ctx, old)
}

func listMonitors(ctx context.Context, u *unit, in ListMonitorsInput) (Page[Monitor], error) {
	result := Page[Monitor]{Items: []Monitor{}}
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
		p := "$" + strconv.Itoa(len(args))
		where = append(where, "(name ILIKE "+p+" OR objective ILIKE "+p+")")
	}
	clause := strings.Join(where, " AND ")
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.monitors WHERE "+clause, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	args = append(args, limit, offset)
	rows, e := u.tx.Query(ctx, "SELECT "+monitorCols+" FROM life.monitors WHERE "+clause+" ORDER BY next_at, id LIMIT $"+strconv.Itoa(len(args)-1)+" OFFSET $"+strconv.Itoa(len(args)), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		m, e := scanMonitor(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, m)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}
