package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Wake is one thing to wake the agent for. The engine describes it; a Waker turns it into a Temporal Schedule exactly as
// the harness's arm_wake does, so a reminder and an agent-armed wake are the same mechanism (ADR 0003).
type Wake struct {
	Name      string // stable per record, e.g. reminder-<uuid>; the waker prefixes it with the user scope
	Kind      string // reminder | instruction | routine
	Subject   string // <entity>:<id>, the record the wake is about
	Objective string
	Why       string
	At        time.Time
	// SessionKey is the session the wake wakes: the one the record was created from. Empty means the waker's default.
	SessionKey string
}

type Waker interface {
	// Arm creates the wake, replacing any existing one with the same Name on the same session.
	Arm(ctx context.Context, w Wake) error
	// Cancel removes it; a wake that does not exist is not an error. session is where it was armed ("" for the default).
	Cancel(ctx context.Context, session, name string) error
	// Exists reports whether the wake is still there, so a Schedule deleted behind the engine's back is armed again.
	Exists(ctx context.Context, session, name string) (bool, error)
}

// wakeGrace is how long after its instant a wake is left alone before the record is moved on to its next occurrence;
// re-arming replaces the Schedule, and doing that in the instant it is about to fire could drop the firing.
const wakeGrace = 2 * time.Minute

// UseWaker enables wakes without the background loop; the caller runs SyncWakes itself (tests).
func (s *Service) UseWaker(w Waker) { s.waker = w }

// SetWaker enables wakes and starts syncing. Without one, records are stored but nothing fires them.
func (s *Service) SetWaker(ctx context.Context, w Waker, every time.Duration) {
	s.UseWaker(w)
	s.nudge = make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			if err := s.SyncWakes(ctx); err != nil {
				slog.Error("wake sync failed", "error", err)
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

// wakeItem is one record's wake: what it should be armed for, and what it is armed for.
type wakeItem struct {
	table, id    string
	want         *time.Time
	armed        *time.Time
	session      string // where it should wake ("" = default); armedSession is where it is armed
	armedSession string
	wake         func(at time.Time) Wake
	moved        string // extra SQL run, with ($1 id, $2 want), when the wake moves on to a later instant
}

func wakeName(kind, id string) string { return kind + "-" + id }

// SyncWakes makes every record's Schedule match its row. Safe to run any time, from any replica.
// ponytail: no cross-replica lock; two replicas may arm the same wake twice, which Arm absorbs by replacing.
func (s *Service) SyncWakes(ctx context.Context) error {
	if s.waker == nil {
		return nil
	}
	var items []wakeItem
	for _, load := range []func(context.Context, time.Time) ([]wakeItem, error){s.reminderWakes, s.scheduleWakes, s.routineWakes, s.monitorWakes} {
		got, err := load(ctx, time.Now())
		if err != nil {
			return err
		}
		items = append(items, got...)
	}
	var failed error
	now := time.Now()
	for _, it := range items {
		name := it.wake(now).Name
		if same(it.want, it.armed) && it.session == it.armedSession && !(it.want != nil && it.want.After(now) && s.missing(ctx, it.session, name)) {
			continue
		}
		var err error
		if it.armed != nil && it.armedSession != it.session {
			err = s.waker.Cancel(ctx, it.armedSession, name) // it was created on another session; take it off that one
		}
		if err == nil && it.want == nil {
			err = s.waker.Cancel(ctx, it.session, name)
		} else if err == nil {
			w := it.wake(*it.want)
			w.SessionKey = it.session
			err = s.waker.Arm(ctx, w)
		}
		if err == nil {
			_, err = s.pool.Exec(ctx, `UPDATE life.`+it.table+` SET armed_for=$2, armed_session=CASE WHEN $2::timestamptz IS NULL THEN NULL ELSE NULLIF($3,'') END WHERE id=$1`, it.id, it.want, it.session)
		}
		if err == nil && it.want != nil && it.moved != "" {
			_, err = s.pool.Exec(ctx, it.moved, it.id, it.want)
		}
		if err != nil {
			slog.Error("could not sync wake", "table", it.table, "id", it.id, "error", err)
			failed = err // keep going; the next pass retries this one
		}
	}
	return failed
}

func (s *Service) missing(ctx context.Context, session, name string) bool {
	ok, err := s.waker.Exists(ctx, session, name)
	return err == nil && !ok // if Temporal cannot answer, assume it is there rather than churn
}

func same(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// --- reminders ---

type wakeRow struct {
	id, title, state, tz   string
	session, armedSession  string
	due, snoozed, armedFor *time.Time
	deleted                bool
	rec                    *Recurrence
}

func reminderWakeName(id string) string { return wakeName("reminder", id) }

// desiredWake is the instant a reminder's wake should be armed for, or nil for none. It depends only on the row, which
// is what makes syncing idempotent.
func desiredWake(r wakeRow, now time.Time) *time.Time {
	if r.deleted || r.state != "pending" || r.due == nil {
		return nil
	}
	at := *r.due
	if r.snoozed != nil {
		at = *r.snoozed
	}
	if !at.After(now) {
		if r.rec != nil && r.snoozed == nil {
			if at.After(now.Add(-wakeGrace)) {
				return &at // due this instant; let the Schedule fire before moving on
			}
			loc, _ := zone(r.tz)
			at = nextOccurrence(at, now, *r.rec, loc)
		} else if r.armedFor == nil && now.Sub(at) < 10*time.Minute {
			at = now.Add(2 * time.Second) // just missed it; fire now. Older than that it stays overdue on the list.
		} else {
			return r.armedFor // already fired (or long overdue): leave the schedule alone
		}
	}
	return &at
}

func reminderObjective(id, title string) string {
	return fmt.Sprintf("The user's reminder %q (Life reminder %s) is due. Open it with the Life reminders_get tool. If it is still pending, "+
		"gather anything that helps (its notes, a linked goal or record, relevant memory), then call notify_user for the user with "+
		"entity \"reminder\" and id %q, a short title and a one-line body. If it is already done or deleted, do nothing and say nothing.", title, id, id)
}

func (s *Service) reminderWakes(ctx context.Context, now time.Time) ([]wakeItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,title,state,time_zone,due_at,snoozed_until,armed_for,deleted_at IS NOT NULL,recurrence,coalesce(session_id,''),coalesce(armed_session,'')
		FROM life.reminders WHERE armed_for IS NOT NULL OR (state='pending' AND deleted_at IS NULL AND due_at IS NOT NULL)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []wakeItem
	for rows.Next() {
		var r wakeRow
		var rec []byte
		if err = rows.Scan(&r.id, &r.title, &r.state, &r.tz, &r.due, &r.snoozed, &r.armedFor, &r.deleted, &rec, &r.session, &r.armedSession); err != nil {
			return nil, err
		}
		if len(rec) > 0 {
			r.rec = new(Recurrence)
			if err = json.Unmarshal(rec, r.rec); err != nil {
				return nil, err
			}
		}
		items = append(items, wakeItem{table: "reminders", id: r.id, want: desiredWake(r, now), armed: r.armedFor, session: sessionScope(r.session), armedSession: r.armedSession,
			// Once a repeating reminder has fired, its due time moves on to the next occurrence. Bookkeeping, not an edit: the
			// version holds, so a screen that already loaded it can still complete it.
			moved: `UPDATE life.reminders SET due_at=$2 WHERE id=$1 AND recurrence IS NOT NULL AND snoozed_until IS NULL AND $2 > due_at`,
			wake: func(at time.Time) Wake {
				return Wake{Name: reminderWakeName(r.id), Kind: "reminder", Subject: "reminder:" + r.id,
					Objective: reminderObjective(r.id, r.title), Why: "A reminder you set for the user is due.", At: at}
			}})
	}
	return items, rows.Err()
}

// --- schedules (recurring prompts for the agent) ---

type scheduleRow struct {
	id, name, instruction, tz string
	rec                       Recurrence
	enabled, deleted          bool
	next                      time.Time
	armed                     *time.Time
	session, armedSession     string
	goalID, goalTitle         string // set when the schedule is a goal's check-in
}

func desiredScheduleWake(r scheduleRow, now time.Time) *time.Time {
	if r.deleted || !r.enabled {
		return nil
	}
	at := r.next
	if !at.After(now) {
		if at.After(now.Add(-wakeGrace)) {
			return &at
		}
		loc, _ := zone(r.tz)
		at = nextOccurrence(at, now, r.rec, loc)
	}
	return &at
}

func (s *Service) scheduleWakes(ctx context.Context, now time.Time) ([]wakeItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id::text,s.name,s.instruction,s.time_zone,s.recurrence,s.enabled,s.deleted_at IS NOT NULL,s.next_at,s.armed_for,coalesce(s.session_id,''),coalesce(s.armed_session,''),
		coalesce(g.id::text,''),coalesce(g.title,'')
		FROM life.schedules s LEFT JOIN life.goals g ON s.link->>'entity'='goal' AND g.id::text = s.link->>'id'
		WHERE s.armed_for IS NOT NULL OR (s.enabled AND s.deleted_at IS NULL)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []wakeItem
	for rows.Next() {
		var r scheduleRow
		var rec []byte
		if err = rows.Scan(&r.id, &r.name, &r.instruction, &r.tz, &rec, &r.enabled, &r.deleted, &r.next, &r.armed, &r.session, &r.armedSession, &r.goalID, &r.goalTitle); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(rec, &r.rec); err != nil {
			return nil, err
		}
		items = append(items, wakeItem{table: "schedules", id: r.id, want: desiredScheduleWake(r, now), armed: r.armed, session: sessionScope(r.session), armedSession: r.armedSession,
			// Once the occurrence has fired, the row records it and shows the next one.
			moved: `UPDATE life.schedules SET last_fired_at=CASE WHEN next_at<=now() THEN next_at ELSE last_fired_at END, next_at=$2 WHERE id=$1 AND $2 > next_at`,
			wake: func(at time.Time) Wake {
				if r.goalID != "" {
					return Wake{Name: wakeName("schedule", r.id), Kind: "goal_checkin", Subject: "goal:" + r.goalID, At: at,
						Objective: goalCheckInObjective(r.goalID, r.goalTitle, r.id, r.instruction),
						Why:       "A regular check-in the user set on one of their goals is due."}
				}
				return Wake{Name: wakeName("schedule", r.id), Kind: "instruction", Subject: "schedule:" + r.id,
					Objective: fmt.Sprintf("The user's recurring request %q (Life schedule %s) is due now. Their instruction: %s\n"+
						"Do it. If there is something the user should see or act on, call notify_user with entity \"schedule\" and id %q, a short title and a one-line body.",
						r.name, r.id, r.instruction, r.id),
					Why: "A recurring request the user set is due.", At: at}
			}})
	}
	return items, rows.Err()
}

// --- routines (the nudge at the person's chosen time) ---

type routineRow struct {
	id, title, short, tz  string
	cues                  []string
	weekdays              []int16
	armed                 *time.Time
	session, armedSession string
	deleted               bool
	count, target         int
}

// desiredRoutineWake is the next cue, on a day the routine applies, in the routine's own time zone. Whether the day's count
// has already reached the target is the agent's check at fire time (routines_status), not the schedule's.
func desiredRoutineWake(r routineRow, now time.Time) *time.Time {
	if r.deleted || len(r.cues) == 0 {
		return nil
	}
	loc, err := zone(r.tz)
	if err != nil {
		return nil
	}
	// Left alone for the grace window after it is due, as with the others.
	from := now.Add(-wakeGrace)
	local := from.In(loc)
	var best *time.Time
	for d := 0; d < 9 && best == nil; d++ {
		day := local.AddDate(0, 0, d)
		if len(r.weekdays) > 0 && !hasDay(r.weekdays, int(day.Weekday())) {
			continue
		}
		for _, cue := range r.cues {
			clock, err := time.Parse("15:04", cue)
			if err != nil {
				continue
			}
			t := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, loc).UTC()
			if t.After(from) && (best == nil || t.Before(*best)) {
				best = &t
			}
		}
	}
	return best
}

func hasDay(days []int16, d int) bool {
	for _, x := range days {
		if int(x) == d {
			return true
		}
	}
	return false
}

func (s *Service) routineWakes(ctx context.Context, now time.Time) ([]wakeItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,title,short_title,time_zone,cues,weekdays,armed_for,deleted_at IS NOT NULL,coalesce(session_id,''),coalesce(armed_session,'')
		FROM life.routines WHERE armed_for IS NOT NULL OR (cardinality(cues) > 0 AND deleted_at IS NULL)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []wakeItem
	for rows.Next() {
		var r routineRow
		if err = rows.Scan(&r.id, &r.title, &r.short, &r.tz, &r.cues, &r.weekdays, &r.armed, &r.deleted, &r.session, &r.armedSession); err != nil {
			return nil, err
		}
		items = append(items, wakeItem{table: "routines", id: r.id, want: desiredRoutineWake(r, now), armed: r.armed, session: sessionScope(r.session), armedSession: r.armedSession, wake: func(at time.Time) Wake {
			return Wake{Name: wakeName("routine", r.id), Kind: "routine", Subject: "routine:" + r.id,
				Objective: fmt.Sprintf("It is the %s nudge time the user chose for their routine %q (Life routine %s). Check today with the Life "+
					"routines_status tool. If it applies today and today's count has not reached the target, call notify_user with entity \"routine\" and id %q, "+
					"a kind one-line nudge that says how far along they are (for example 1 of 3) and offers the short version (%s) as an easy option. "+
					"If the day is already done or it does not apply today, do nothing and say nothing.", cueLabel(at, r.tz), r.title, r.id, r.id, r.short),
				Why: "The user asked to be nudged about a routine at this time.", At: at}
		}})
	}
	return items, rows.Err()
}

func goalCheckInObjective(goalID, title, scheduleID, instruction string) string {
	return fmt.Sprintf("It is time for the user's regular check-in on their goal %q (Life goal %s, from Life schedule %s). %s\n"+
		"Read the goal with the Life goals_get tool and this week's commitments with commitments_list. Compare progress with the weekly target. "+
		"If they are behind or have stalled, do not pressure them: offer one smaller next step (steps_add if they agree, or suggest it) and ask what got in the way. "+
		"If they are on track, say so in a line. Either way call notify_user with entity \"goal\" and id %q, a short title and a kind one-line body.",
		title, goalID, scheduleID, instruction, goalID)
}

// --- monitors ---

type monitorRow struct {
	id, name, objective, session, armedSession string
	every                                      time.Duration
	enabled, deleted                           bool
	next                                       time.Time
	armed                                      *time.Time
}

func desiredMonitorWake(r monitorRow, now time.Time) *time.Time {
	if r.deleted || !r.enabled {
		return nil
	}
	at := r.next
	if !at.After(now) {
		if at.After(now.Add(-wakeGrace)) {
			return &at
		}
		// Missed checks are skipped, not replayed: the next one is the first slot still ahead.
		missed := now.Sub(at)/r.every + 1
		at = at.Add(missed * r.every)
	}
	return &at
}

func (s *Service) monitorWakes(ctx context.Context, now time.Time) ([]wakeItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,name,objective,every_minutes,enabled,deleted_at IS NOT NULL,next_at,armed_for,coalesce(session_id,''),coalesce(armed_session,'')
		FROM life.monitors WHERE armed_for IS NOT NULL OR (enabled AND deleted_at IS NULL)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []wakeItem
	for rows.Next() {
		var r monitorRow
		var every int
		if err = rows.Scan(&r.id, &r.name, &r.objective, &every, &r.enabled, &r.deleted, &r.next, &r.armed, &r.session, &r.armedSession); err != nil {
			return nil, err
		}
		r.every = time.Duration(every) * time.Minute
		items = append(items, wakeItem{table: "monitors", id: r.id, want: desiredMonitorWake(r, now), armed: r.armed, session: sessionScope(r.session), armedSession: r.armedSession,
			moved: `UPDATE life.monitors SET next_at=$2 WHERE id=$1 AND $2 > next_at`,
			wake: func(at time.Time) Wake {
				return Wake{Name: wakeName("monitor", r.id), Kind: "monitor", Subject: "monitor:" + r.id, At: at,
					Objective: fmt.Sprintf("Time to check the user's monitor %q (Life monitor %s). What they asked you to watch: %s\n"+
						"Check it now, using whatever tools you need. Then call the Life monitors_record tool with one line on what you found and triggered true only if the user should hear about it. "+
						"If triggered, also call notify_user with entity \"monitor\" and id %q, a short title and a one-line body. If not, say nothing to the user.", r.name, r.id, r.objective, r.id),
					Why: "A monitor the user set is due for a check."}
			}})
	}
	return items, rows.Err()
}

// cueLabel is the clock time of an instant in the routine's own zone, for the wake's wording.
func cueLabel(at time.Time, tz string) string {
	loc, err := zone(tz)
	if err != nil {
		return at.UTC().Format("15:04")
	}
	return at.In(loc).Format("15:04")
}
