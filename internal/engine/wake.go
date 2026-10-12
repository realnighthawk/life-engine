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
	Kind      string // reminder
	Subject   string // <entity>:<id>, the record the wake is about
	Objective string
	Why       string
	At        time.Time
}

type Waker interface {
	// Arm creates the wake, replacing any existing one with the same Name.
	Arm(ctx context.Context, w Wake) error
	// Cancel removes it; a wake that does not exist is not an error.
	Cancel(ctx context.Context, name string) error
}

// SetWaker enables wakes and starts syncing. Without one, reminders are stored but nothing fires them.
func (s *Service) SetWaker(ctx context.Context, w Waker, every time.Duration) {
	s.waker = w
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

type wakeRow struct {
	id, title, state, tz   string
	due, snoozed, armedFor *time.Time
	deleted                bool
	rec                    *Recurrence
}

func reminderWakeName(id string) string { return "reminder-" + id }

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

// SyncWakes makes every reminder's Schedule match its row. Safe to run any time, from any replica.
// ponytail: no cross-replica lock; two replicas may arm the same wake twice, which Arm absorbs by replacing.
func (s *Service) SyncWakes(ctx context.Context) error {
	if s.waker == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,title,state,time_zone,due_at,snoozed_until,armed_for,deleted_at IS NOT NULL,recurrence
		FROM life.reminders WHERE armed_for IS NOT NULL OR (state='pending' AND deleted_at IS NULL AND due_at IS NOT NULL)`)
	if err != nil {
		return err
	}
	var all []wakeRow
	for rows.Next() {
		var r wakeRow
		var rec []byte
		if err = rows.Scan(&r.id, &r.title, &r.state, &r.tz, &r.due, &r.snoozed, &r.armedFor, &r.deleted, &rec); err != nil {
			rows.Close()
			return err
		}
		if len(rec) > 0 {
			r.rec = new(Recurrence)
			if err = json.Unmarshal(rec, r.rec); err != nil {
				rows.Close()
				return err
			}
		}
		all = append(all, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var failed error
	for _, r := range all {
		want := desiredWake(r, time.Now())
		if same(want, r.armedFor) {
			continue
		}
		if want == nil {
			err = s.waker.Cancel(ctx, reminderWakeName(r.id))
		} else {
			err = s.waker.Arm(ctx, Wake{Name: reminderWakeName(r.id), Kind: "reminder", Subject: "reminder:" + r.id,
				Objective: reminderObjective(r.id, r.title), Why: "A reminder you set for the user is due.", At: *want})
		}
		if err == nil {
			_, err = s.pool.Exec(ctx, `UPDATE life.reminders SET armed_for=$2 WHERE id=$1`, r.id, want)
		}
		if err != nil {
			slog.Error("could not sync reminder wake", "reminder", r.id, "error", err)
			failed = err // keep going; the next pass retries this one
		}
	}
	return failed
}

func same(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
