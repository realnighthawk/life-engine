package engine

import (
	"context"
	"time"
)

const settingsID = "00000000-0000-0000-0000-000000000001"

type Settings struct {
	ID        string    `json:"id"`
	TimeZone  string    `json:"time_zone" jsonschema:"IANA zone; every day-based operation (today's plan, closing the day) uses this zone"`
	WorkStart string    `json:"work_start" jsonschema:"HH:MM"`
	WorkEnd   string    `json:"work_end" jsonschema:"HH:MM"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}
type SettingsInput struct {
	Meta
	ExpectedVersion int64  `json:"expected_version"`
	TimeZone        string `json:"time_zone" jsonschema:"IANA zone such as Europe/London"`
	WorkStart       string `json:"work_start" jsonschema:"HH:MM"`
	WorkEnd         string `json:"work_end" jsonschema:"HH:MM"`
}

func (u *unit) getSettings(ctx context.Context) (Settings, error) {
	var s Settings
	err := u.tx.QueryRow(ctx, `SELECT id::text,time_zone,work_start,work_end,version,updated_at FROM life.settings WHERE id=$1`, settingsID).
		Scan(&s.ID, &s.TimeZone, &s.WorkStart, &s.WorkEnd, &s.Version, &s.UpdatedAt)
	s.UpdatedAt = s.UpdatedAt.UTC()
	return s, err
}

func validClock(field, v string) error {
	if _, err := time.Parse("15:04", v); err != nil || len(v) != 5 {
		return invalid(field + " must be HH:MM")
	}
	return nil
}

func setSettings(ctx context.Context, u *unit, in SettingsInput) (Settings, error) {
	old, e := u.getSettings(ctx)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if _, e = zone(in.TimeZone); e != nil {
		return old, e
	}
	if e = validClock("work_start", in.WorkStart); e != nil {
		return old, e
	}
	if e = validClock("work_end", in.WorkEnd); e != nil {
		return old, e
	}
	if in.WorkEnd <= in.WorkStart {
		return old, invalid("work_end must be after work_start")
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.settings SET time_zone=$2,work_start=$3,work_end=$4,version=version+1,updated_at=now() WHERE id=$1`, settingsID, in.TimeZone, in.WorkStart, in.WorkEnd); e != nil {
		return old, e
	}
	s, e := u.getSettings(ctx)
	if e != nil {
		return s, e
	}
	return s, u.audit(ctx, "settings", settingsID, old, s)
}

// today is the person's current local calendar date (YYYY-MM-DD) and zone, from the database clock.
func (u *unit) today(ctx context.Context) (string, *time.Location, time.Time, error) {
	s, err := u.getSettings(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	loc, err := zone(s.TimeZone)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	now, err := u.now(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return now.In(loc).Format("2006-01-02"), loc, now, nil
}
