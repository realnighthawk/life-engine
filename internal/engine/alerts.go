package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// An alert is when a task or an event tells its owner. The engine owns the clock (project-hub ADR 0008): the record says
// what, and the engine keeps a Temporal Schedule armed to match it, creating, updating in place and removing it as the
// record changes. Nothing outside the engine creates a timer.

// AlertSpec is one Schedule a record should have. Name is "main" (the record's own alert) or "snooze" (a one-shot
// deferral).
type AlertSpec struct {
	Name     string        `json:"name"`
	TimeZone string        `json:"time_zone"`
	Once     *time.Time    `json:"once,omitempty"`
	Calendar *CalendarSpec `json:"calendar,omitempty"`
	Interval *IntervalSpec `json:"interval,omitempty"`
	// Start is the first moment the series may fire; nothing earlier does.
	Start time.Time `json:"start"`
}

// CalendarSpec is a wall-clock time in the spec's zone on the given weekdays (0 = Sunday), the given day of the month, or
// every day when both are empty.
type CalendarSpec struct {
	Hour, Minute, Second int
	Weekdays             []int
	DayOfMonth           int
}

// IntervalSpec repeats every Every, aligned so one firing lands on Offset seconds after the epoch.
type IntervalSpec struct {
	Every  time.Duration
	Offset time.Duration
}

// Waker is the engine's side of the clock. Apply creates or updates a Schedule in place; Remove deletes one (absent is not
// an error); Exists reports whether it is there, so one deleted behind the engine's back is armed again.
type Waker interface {
	Apply(ctx context.Context, itemID string, spec AlertSpec) error
	Remove(ctx context.Context, itemID, name string) error
	Exists(ctx context.Context, itemID, name string) (bool, error)
	// Wake delivers one wake into a conversation as a workflow named workflowID, so starting it twice is one wake.
	Wake(ctx context.Context, workflowID string, w FireWake) error
}

// alertGrace is how far past its time a one-shot alert is still armed. Temporal does not fire a calendar time already gone,
// so an alert missed by longer than this is left, and logged by the sync, not retried forever.
const alertGrace = 2 * time.Minute

// recurringAlert is the Schedule a repeating record's alert needs. Temporal's calendar specs read local wall-clock time in
// a zone, which is what a repeat should keep across daylight saving. What they cannot say is "every N weeks on these
// weekdays" or "every N months", and they cannot move a firing to an earlier day, so those are refused here rather than
// armed wrongly. ponytail: every N days or weeks (N > 1, no weekday list) uses a fixed-length interval, so it drifts an hour
// across daylight saving; a monthly day 29-31 skips the shorter months.
func recurringAlert(at time.Time, before int, r Recurrence, loc *time.Location) (AlertSpec, error) {
	anchor := at.In(loc)
	alert := anchor.Add(-time.Duration(before) * time.Minute)
	if alert.Year() != anchor.Year() || alert.YearDay() != anchor.YearDay() {
		return AlertSpec{}, invalid("a repeating record's alert must fall on the same day as the record: use fewer minutes before, or no alert")
	}
	spec := AlertSpec{Name: "main", TimeZone: loc.String(), Start: alert.UTC()}
	cal := CalendarSpec{Hour: alert.Hour(), Minute: alert.Minute(), Second: alert.Second()}
	interval := r.Interval
	if interval < 1 {
		interval = 1
	}
	switch r.Freq {
	case "daily":
		if interval == 1 {
			spec.Calendar = &cal
			return spec, nil
		}
		every := time.Duration(interval) * 24 * time.Hour
		spec.Interval = &IntervalSpec{Every: every, Offset: offsetOf(alert, every)}
	case "weekly":
		switch {
		case interval == 1:
			cal.Weekdays = r.Weekdays
			if len(cal.Weekdays) == 0 {
				cal.Weekdays = []int{int(anchor.Weekday())}
			}
			spec.Calendar = &cal
		case len(r.Weekdays) == 0:
			every := time.Duration(interval) * 7 * 24 * time.Hour
			spec.Interval = &IntervalSpec{Every: every, Offset: offsetOf(alert, every)}
		default:
			return AlertSpec{}, invalid("an alert cannot repeat every other week (or less often) on chosen weekdays; repeat every week, or on one weekday")
		}
	case "monthly":
		if interval != 1 {
			return AlertSpec{}, invalid("an alert cannot repeat every few months; repeat monthly, or leave the alert off")
		}
		cal.DayOfMonth = anchor.Day()
		spec.Calendar = &cal
	}
	return spec, nil
}

func offsetOf(t time.Time, every time.Duration) time.Duration {
	secs := int64(every / time.Second)
	return time.Duration(((t.Unix()%secs)+secs)%secs) * time.Second
}

// scheduledItem is the timing half of a task or an event, read across both tables for the clock. An event's
// cancelled_at is read as done_at, so a cancelled event is treated like a finished task: nothing to fire.
type scheduledItem struct {
	ID, Kind, Subject, Title, Notes, TimeZone, SessionKey string
	At                                                    *time.Time
	Recurrence                                            *Recurrence
	Alert                                                 *int
	SnoozedUntil, DoneAt, DeletedAt                       *time.Time
	IsPriority                                            bool
	Armed                                                 string
}

const scheduledCols = `id::text,kind,subject,title,notes,at,time_zone,recurrence,alert_before_minutes,coalesce(session_key,''),snoozed_until,done_at,is_priority,deleted_at,coalesce(armed,'')`
const scheduledFrom = `(SELECT id::text AS id,'task' AS kind,subject,title,notes,at,time_zone,recurrence,alert_before_minutes,
		coalesce(session_key,'') AS session_key,snoozed_until,done_at,is_priority,deleted_at,coalesce(armed,'') AS armed
		FROM life.tasks
	UNION ALL
	SELECT id::text,'event',subject,title,notes,at,time_zone,recurrence,alert_before_minutes,
		coalesce(session_key,''),snoozed_until,cancelled_at,false,deleted_at,coalesce(armed,'')
		FROM life.events) s`

func scanScheduled(row scanner) (scheduledItem, error) {
	var it scheduledItem
	var rec []byte
	err := row.Scan(&it.ID, &it.Kind, &it.Subject, &it.Title, &it.Notes, &it.At, &it.TimeZone, &rec, &it.Alert, &it.SessionKey,
		&it.SnoozedUntil, &it.DoneAt, &it.IsPriority, &it.DeletedAt, &it.Armed)
	if err != nil {
		return it, err
	}
	if len(rec) > 0 {
		it.Recurrence = new(Recurrence)
		if err = json.Unmarshal(rec, it.Recurrence); err != nil {
			return it, err
		}
	}
	for _, p := range []*time.Time{it.At, it.SnoozedUntil, it.DoneAt, it.DeletedAt} {
		utcPtr(p)
	}
	return it, nil
}

// desiredAlerts is every Schedule a record should have right now: none for a record that is deleted, done or cancelled,
// without an alert, or whose one-shot time has passed.
func desiredAlerts(it scheduledItem, now time.Time) ([]AlertSpec, error) {
	if it.DeletedAt != nil || it.Alert == nil || it.At == nil || it.DoneAt != nil {
		return nil, nil
	}
	loc, e := zone(it.TimeZone)
	if e != nil {
		return nil, e
	}
	var specs []AlertSpec
	snoozed := it.SnoozedUntil != nil && it.SnoozedUntil.After(now)
	if it.Recurrence == nil {
		when := it.At.Add(-time.Duration(*it.Alert) * time.Minute)
		if snoozed {
			when = *it.SnoozedUntil
		}
		if when.Before(now.Add(-alertGrace)) {
			return nil, nil
		}
		return []AlertSpec{{Name: "main", TimeZone: loc.String(), Once: &when, Start: when}}, nil
	}
	main, e := recurringAlert(*it.At, *it.Alert, *it.Recurrence, loc)
	if e != nil {
		return nil, e
	}
	specs = append(specs, main)
	if snoozed {
		until := *it.SnoozedUntil
		specs = append(specs, AlertSpec{Name: "snooze", TimeZone: loc.String(), Once: &until, Start: until})
	}
	return specs, nil
}

func hashSpecs(specs []AlertSpec) string {
	if len(specs) == 0 {
		return ""
	}
	b, _ := json.Marshal(specs)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// UseWaker enables alerts without the background loop; the caller runs SyncAlerts itself (tests).
func (s *Service) UseWaker(w Waker) { s.waker = w }

// SetWaker enables alerts and starts syncing: on every committed write, and on a timer to catch a Schedule deleted behind
// the engine's back. Without a waker, records are stored but nothing fires them.
func (s *Service) SetWaker(ctx context.Context, w Waker, every time.Duration) {
	s.UseWaker(w)
	s.nudge = make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			if err := s.SyncAlerts(ctx); err != nil {
				slog.Error("alert sync failed", "error", err)
			}
			if err := s.SendWatchWakes(ctx); err != nil {
				slog.Error("watch wakes failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.nudge:
			}
		}
	}()
}

// SyncAlerts makes every record's Schedules match its row. Safe to run any time, from any replica: arming is idempotent
// because Apply updates in place.
// ponytail: no cross-replica lock; two replicas may apply the same spec twice, which Apply absorbs.
func (s *Service) SyncAlerts(ctx context.Context) error {
	if s.waker == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, "SELECT "+scheduledCols+" FROM "+scheduledFrom+" WHERE alert_before_minutes IS NOT NULL OR armed IS NOT NULL")
	if err != nil {
		return err
	}
	var entries []scheduledItem
	for rows.Next() {
		it, e := scanScheduled(rows)
		if e != nil {
			rows.Close()
			return e
		}
		entries = append(entries, it)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	now := time.Now()
	var firstErr error
	for _, it := range entries {
		if e := s.syncOne(ctx, it, now); e != nil {
			slog.Error("alert sync failed for a record", "id", it.ID, "error", e)
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	return firstErr
}

func (s *Service) syncOne(ctx context.Context, it scheduledItem, now time.Time) error {
	specs, err := desiredAlerts(it, now)
	if err != nil {
		return fmt.Errorf("record %s: %w", it.ID, err)
	}
	want := hashSpecs(specs)
	present := map[string]bool{}
	for _, sp := range specs {
		present[sp.Name] = true
	}
	if want == it.Armed {
		// Unchanged on paper: check each Schedule is still there, so one removed behind our back is armed again.
		missing := false
		for _, sp := range specs {
			ok, e := s.waker.Exists(ctx, it.ID, sp.Name)
			if e != nil {
				return e
			}
			if !ok {
				missing = true
			}
		}
		if !missing {
			return nil
		}
	}
	for _, sp := range specs {
		if err = s.waker.Apply(ctx, it.ID, sp); err != nil {
			return err
		}
	}
	for _, name := range []string{"main", "snooze"} {
		if !present[name] {
			if err = s.waker.Remove(ctx, it.ID, name); err != nil {
				return err
			}
		}
	}
	table := "life.tasks"
	if it.Kind == "event" {
		table = "life.events"
	}
	_, err = s.pool.Exec(ctx, "UPDATE "+table+" SET armed=NULLIF($2,'') WHERE id=$1", it.ID, want)
	return err
}

// Fire is what an alert coming due asks of the engine, called by the workflow its Schedule starts. It decides what is to be
// delivered and returns it; delivering is the workflow's job, as an activity with retries. A person's record returns the
// notice for the gateway to store and push (ADR 0008); an agent's returns the conversation to wake and what to wake it
// with. A record that is gone, done, deleted or already cleared for this occurrence fires nothing.
type FireResult struct {
	Fired bool   `json:"fired"`
	Skip  string `json:"skip,omitempty"`
	// Notify is set for a person's record: post it to the gateway.
	Notify *FireNotice `json:"notify,omitempty"`
	// Wake is set for an agent's record: deliver it into SessionKey.
	Wake *FireWake `json:"wake,omitempty"`
}

// FireNotice is a person's alert as the gateway's events intake takes it. EventID names the occurrence, so a retried delivery
// is stored once.
type FireNotice struct {
	EventID    string `json:"event_id"`
	Detail     string `json:"detail"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Priority   string `json:"priority"`
	LinkEntity string `json:"link_entity"`
	LinkID     string `json:"link_id"`
}
type FireWake struct {
	SessionKey string `json:"session_key"`
	WakeID     string `json:"wake_id"`
	Objective  string `json:"objective"`
	Why        string `json:"why,omitempty"`
}

func (s *Service) Fire(ctx context.Context, itemID string, scheduled time.Time) (FireResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FireResult{}, err
	}
	defer tx.Rollback(context.Background())
	it, err := scanScheduled(tx.QueryRow(ctx, "SELECT "+scheduledCols+" FROM "+scheduledFrom+" WHERE id=$1 LIMIT 1", itemID))
	if isNoRows(err) {
		return FireResult{Skip: "the record is gone"}, nil
	}
	if err != nil {
		return FireResult{}, err
	}
	switch {
	case it.DeletedAt != nil:
		return FireResult{Skip: "the record is deleted"}, nil
	case it.DoneAt != nil:
		return FireResult{Skip: "the record is done or cancelled"}, nil
	case it.Alert == nil:
		return FireResult{Skip: "the record no longer has an alert"}, nil
	}
	// The occurrence the alert is for starts after the alert by the lead; a cleared occurrence is not announced.
	occurrence := scheduled.Add(time.Duration(*it.Alert) * time.Minute).Truncate(time.Second)
	if it.Kind == "task" {
		var cleared bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM life.links l JOIN life.activities a ON a.id=l.from_id
			WHERE l.from_kind='activity' AND l.relation='fulfils' AND l.to_kind='task' AND l.to_id=$1 AND a.occurred_at=$2)`, it.ID, occurrence).Scan(&cleared); err != nil {
			return FireResult{}, err
		}
		if cleared {
			return FireResult{Skip: "the occurrence is already cleared"}, nil
		}
	}
	if it.Subject == "agent" {
		if it.SessionKey == "" {
			return FireResult{}, fmt.Errorf("agent %s %s has an alert but no conversation to wake", it.Kind, it.ID)
		}
		objective := it.Title
		if it.Notes != "" {
			objective += "\n\n" + it.Notes
		}
		return FireResult{Fired: true, Wake: &FireWake{SessionKey: it.SessionKey, WakeID: fmt.Sprintf("item:%s@%d", it.ID, occurrence.Unix()),
			Objective: objective, Why: "This is your own " + it.Kind + ", set to come due now."}}, nil
	}
	loc, _ := zone(it.TimeZone)
	body := it.Notes
	if it.At != nil {
		when := occurrence.In(loc).Format("Mon 15:04")
		if body == "" {
			body = when
		} else {
			body = when + " · " + body
		}
	}
	priority := "normal"
	if it.IsPriority {
		priority = "high"
	}
	detail := "Your " + it.Kind + " " + strconv.Quote(it.Title) + " is due."
	if it.Kind == "event" {
		detail = "Your event " + strconv.Quote(it.Title) + " is starting."
	}
	return FireResult{Fired: true, Notify: &FireNotice{
		EventID: fmt.Sprintf("item:%s@%d", it.ID, occurrence.Unix()), Detail: detail,
		Title: it.Title, Body: body, Priority: priority, LinkEntity: it.Kind, LinkID: it.ID}}, nil
}
