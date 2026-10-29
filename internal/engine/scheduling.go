package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TimingData is the timing half a task and an event share: when it is, how long, in which zone, how it repeats, when it
// alerts, and whose it is. The two records keep separate tables and lifecycles; only this shape is shared.
type scanner interface{ Scan(...any) error }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Progress counts a record's linked work: done and total.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type TimingData struct {
	At                 *string         `json:"at,omitempty" jsonschema:"RFC 3339 instant: a task's deadline or cue, an event's start. An event requires it"`
	Minutes            *int            `json:"minutes,omitempty" jsonschema:"how long it takes, 1-1440"`
	TimeZone           string          `json:"time_zone,omitempty" jsonschema:"IANA zone the person lives in; a repeat keeps this zone's wall-clock time. Default UTC"`
	Recurrence         *Recurrence     `json:"recurrence,omitempty" jsonschema:"optional repeat; requires at"`
	AlertBeforeMinutes *int            `json:"alert_before_minutes,omitempty" jsonschema:"tell the owner this many minutes before at (0 = at the time), up to 10080; requires at. Omit for no alert"`
	Subject            string          `json:"subject,omitempty" jsonschema:"person (default), or agent for a record of your own, which the person's screens do not show; an agent's record with an alert wakes the conversation you create it from"`
	Origin             string          `json:"origin,omitempty" jsonschema:"person (default) or agent: who authored it"`
	DerivedFrom        json.RawMessage `json:"derived_from,omitempty" jsonschema:"when an agent promoted a memory candidate: {bank_id, record_id, snapshot}"`
}

type timingValues struct {
	at              *time.Time
	minutes, alert  *int
	tz              string
	rec             *Recurrence
	subject, origin string
	derived         json.RawMessage
}

func (d TimingData) values(requireAt bool) (timingValues, error) {
	v := timingValues{tz: d.TimeZone, minutes: d.Minutes, alert: d.AlertBeforeMinutes, subject: d.Subject, origin: d.Origin}
	if v.tz == "" {
		v.tz = "UTC"
	}
	loc, e := zone(v.tz)
	if e != nil {
		return v, e
	}
	if v.subject == "" {
		v.subject = "person"
	}
	if !oneOf(v.subject, "person", "agent") {
		return v, invalid("subject must be person or agent")
	}
	if v.origin == "" {
		v.origin = "person"
	}
	if !oneOf(v.origin, "person", "agent") {
		return v, invalid("origin must be person or agent")
	}
	if v.minutes != nil && (*v.minutes < 1 || *v.minutes > 1440) {
		return v, invalid("minutes must be 1-1440")
	}
	if d.At != nil {
		t, e := instant("at", *d.At)
		if e != nil {
			return v, e
		}
		v.at = &t
	}
	if requireAt && v.at == nil {
		return v, invalid("an event happens at a time: give at")
	}
	if d.Recurrence != nil {
		if v.at == nil {
			return v, invalid("recurrence requires at")
		}
		r := *d.Recurrence
		if e := r.normalize(); e != nil {
			return v, e
		}
		v.rec = &r
	}
	if v.alert != nil {
		if v.at == nil {
			return v, invalid("alert_before_minutes requires at")
		}
		if *v.alert < 0 || *v.alert > 10080 {
			return v, invalid("alert_before_minutes must be 0-10080")
		}
		if v.rec != nil {
			if _, e = recurringAlert(*v.at, *v.alert, *v.rec, loc); e != nil {
				return v, e
			}
		}
	}
	derived, e := derivedFrom(d.DerivedFrom)
	if e != nil {
		return v, e
	}
	v.derived = derived
	return v, nil
}

// sessionFor is the conversation an agent's record wakes: the one it was created from. A person's record needs none.
func (u *unit) sessionFor(v timingValues) (string, error) {
	if v.subject != "agent" {
		return "", nil
	}
	if u.session == "" {
		if v.alert != nil {
			return "", invalid("an agent's record with an alert wakes the conversation it was created from; create it from a conversation")
		}
		return "", nil
	}
	return sessionScope(u.session), nil
}

func jsonOrNil(v any) any {
	if r, ok := v.(*Recurrence); ok && r == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func trimTitleNotes(title, notes string) (string, string, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 {
		return title, notes, invalid("title must be 1-200 characters")
	}
	if len(notes) > 2000 {
		return title, notes, invalid("notes must be at most 2000 characters")
	}
	return title, notes, nil
}
