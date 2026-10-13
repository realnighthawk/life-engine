package engine_test

import (
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestRoutineTargetCuesAndCounting(t *testing.T) {
	s := integrationService(t)
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Drink water", "target_per_day": 3,
		"cues": []string{"17:00", "09:00", "13:00"}, "time_zone": "UTC"})
	if r.TargetPerDay != 3 || len(r.Cues) != 3 || r.Cues[0] != "09:00" || r.Cues[2] != "17:00" {
		t.Fatalf("created: %+v", r)
	}
	errorCode(t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Too many", "target_per_day": 13}, "invalid_input")
	errorCode(t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Twice", "cues": []string{"09:00", "09:00"}}, "invalid_input")
	errorCode(t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Bad", "cues": []string{"25:00"}}, "invalid_input")
	errorCode(t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Nine", "cues": []string{"01:00", "02:00", "03:00", "04:00", "05:00", "06:00", "07:00", "08:00", "09:00"}}, "invalid_input")
	plain := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Stretch"})
	if plain.TargetPerDay != 1 || plain.Cues == nil || len(plain.Cues) != 0 {
		t.Fatalf("defaults: %+v", plain)
	}

	log := func(delta int) engine.RoutineStatus {
		return execute[engine.RoutineStatus](t, s, "routines_log", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "delta": delta})
	}
	st := log(1)
	if st.Count != 1 || st.Target != 3 || st.Done || st.Hint != "1 of 3 today so far." {
		t.Fatalf("one: %+v", st)
	}
	log(1)
	st = log(1)
	if st.Count != 3 || !st.Done || st.Streak != 1 {
		t.Fatalf("three: %+v", st)
	}
	st = log(-1)
	if st.Count != 2 || st.Done {
		t.Fatalf("taking one back must reopen the day: %+v", st)
	}
	errorCode(t, s, "routines_log", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "delta": 0}, "invalid_input")
	errorCode(t, s, "routines_log", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "day": time.Now().AddDate(0, 0, 2).Format("2006-01-02")}, "invalid_input")

	// The older whole-day check-in completes the day, and none clears it.
	st = execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "variant": "short"})
	if st.Count != 3 || !st.Done || st.Variant != "short" {
		t.Fatalf("whole-day check-in: %+v", st)
	}
	st = execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "variant": "none"})
	if st.Count != 0 || st.Done {
		t.Fatalf("cleared: %+v", st)
	}
}

func TestRoutineHistoryIsARateNotAStreakThatResets(t *testing.T) {
	s := integrationService(t)
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Walk"})
	day := func(back int) string { return time.Now().UTC().AddDate(0, 0, -back).Format("2006-01-02") }
	// Done 6, 5, 4, 2 and 1 days ago (missed 3 days ago); nothing today yet.
	for _, back := range []int{6, 5, 4, 2, 1} {
		execute[engine.RoutineStatus](t, s, "routine_checkins_set", map[string]any{"idempotency_key": key(), "routine_id": r.ID, "day": day(back), "variant": "full"})
	}
	h := execute[engine.RoutineHistory](t, s, "routines_history", map[string]any{"routine_id": r.ID})
	if len(h.Days) != 30 || h.Days[29].Day != day(0) || h.Days[28].Day != day(1) {
		t.Fatalf("days must be oldest first and end today: %d first %s last %s", len(h.Days), h.Days[0].Day, h.Days[29].Day)
	}
	if h.Last7.Scheduled != 7 || h.Last7.Done != 5 {
		t.Fatalf("5 of the last 7: %+v", h.Last7)
	}
	if h.Streak != 2 || h.BestStreak != 3 {
		t.Fatalf("a missed day costs the streak but the best run remains: streak %d best %d", h.Streak, h.BestStreak)
	}
	if !h.Days[26].Scheduled || h.Days[26].Done || h.Days[28].Count != 1 {
		t.Fatalf("day detail: %+v %+v", h.Days[26], h.Days[28])
	}
	errorCode(t, s, "routines_history", map[string]any{"routine_id": r.ID, "days": 3}, "invalid_input")
}

func TestRoutineCuesArmTheNextOne(t *testing.T) {
	s, f, sync := newSynced(t)
	now := time.Now().UTC()
	soon, later := now.Add(2*time.Hour).Format("15:04"), now.Add(5*time.Hour).Format("15:04")
	r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Drink water", "target_per_day": 3, "cues": []string{later, soon}, "time_zone": "UTC"})
	sync()
	w, ok := f.armed["routine-"+r.ID]
	if !ok || w.At.UTC().Format("15:04") != soon || w.Kind != "routine" {
		t.Fatalf("must arm the earliest cue still ahead: %+v", f.armed)
	}
}

// A routine's "today" is today where its person is, not in the account's default zone. Kiritimati (UTC+14) and Pago Pago
// (UTC-11) are 25 hours apart, so at any moment at least one of them is on a different calendar day from UTC.
func TestRoutineTodayIsInItsOwnTimeZone(t *testing.T) {
	s := integrationService(t)
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago", "America/Los_Angeles"} {
		loc, _ := time.LoadLocation(zone)
		local := time.Now().In(loc)
		r := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Weigh in " + zone, "time_zone": zone, "weekdays": []int{int(local.Weekday())}})
		st := execute[engine.RoutinesStatus](t, s, "routines_status", map[string]any{})
		var found *engine.RoutineStatus
		for i := range st.Items {
			if st.Items[i].Routine.ID == r.ID {
				found = &st.Items[i]
			}
		}
		if found == nil || !found.Scheduled {
			t.Fatalf("%s: a routine set for %s must be scheduled today where it lives: %+v", zone, local.Weekday(), found)
		}
		// Logging with no day lands on the routine's own today, and the history ends on that day.
		logged := execute[engine.RoutineStatus](t, s, "routines_log", map[string]any{"idempotency_key": key(), "routine_id": r.ID})
		h := execute[engine.RoutineHistory](t, s, "routines_history", map[string]any{"routine_id": r.ID, "days": 7})
		if logged.Count != 1 || h.Days[len(h.Days)-1].Day != local.Format("2006-01-02") || h.Days[len(h.Days)-1].Count != 1 {
			t.Fatalf("%s: logged today %s but history ends %+v", zone, local.Format("2006-01-02"), h.Days[len(h.Days)-1])
		}
	}
}

// A goal is served by routines that already exist; they are linked, not copied into its steps.
func TestGoalsAreServedByLinkedRoutines(t *testing.T) {
	s := integrationService(t)
	water := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Drink water", "target_per_day": 3})
	walk := execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Walk"})
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Lose 5 kg",
		"steps": []map[string]any{{"title": "Weigh in once"}}, "routine_ids": []string{water.ID}})
	if len(g.RoutineIDs) != 1 || g.RoutineIDs[0] != water.ID || len(g.Steps) != 1 {
		t.Fatalf("linked at creation: %+v", g)
	}
	// Linking is idempotent, and one routine can serve more than one goal.
	g = execute[engine.Goal](t, s, "goals_link_routine", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "routine_id": walk.ID})
	again := execute[engine.Goal](t, s, "goals_link_routine", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "routine_id": walk.ID})
	if len(g.RoutineIDs) != 2 || len(again.RoutineIDs) != 2 {
		t.Fatalf("link twice must stay one link: %+v %+v", g.RoutineIDs, again.RoutineIDs)
	}
	other := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Sleep better", "routine_ids": []string{water.ID}})
	if len(other.RoutineIDs) != 1 {
		t.Fatalf("a routine serves several goals: %+v", other.RoutineIDs)
	}
	errorCode(t, s, "goals_link_routine", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "routine_id": "ffffffff-ffff-4fff-8fff-ffffffffffff"}, "not_found")
	errorCode(t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Bad", "routine_ids": []string{"ffffffff-ffff-4fff-8fff-ffffffffffff"}}, "not_found")

	// Unlinking removes only the link; deleting a routine drops it from the goal without touching the goal.
	g = execute[engine.Goal](t, s, "goals_unlink_routine", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "routine_id": walk.ID})
	if len(g.RoutineIDs) != 1 || g.RoutineIDs[0] != water.ID {
		t.Fatalf("unlinked: %+v", g.RoutineIDs)
	}
	execute[engine.Routine](t, s, "routines_delete", map[string]any{"idempotency_key": key(), "id": water.ID, "expected_version": water.Version})
	got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if len(got.RoutineIDs) != 0 || got.Title != "Lose 5 kg" {
		t.Fatalf("a deleted routine leaves the goal: %+v", got)
	}
	if list := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{}); list.Total != 2 || list.Items[0].RoutineIDs == nil {
		t.Fatalf("list: %+v", list)
	}
}
