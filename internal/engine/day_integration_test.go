package engine_test

import (
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestDayIsAViewOfTasksAndEventsWithOverdueCarriedForward(t *testing.T) {
	s := integrationService(t) // the person's zone is UTC
	today := time.Now().UTC().Truncate(24 * time.Hour)
	at := func(d int, h int) string {
		return today.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
	}

	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Call Sam", "at": at(0, 23)})
	execute[engine.Item](t, s, "calendar_events_create", map[string]any{"idempotency_key": key(), "title": "Standup", "at": at(0, 9), "minutes": 15})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "File taxes", "at": at(-2, 12)}) // overdue
	done := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Old and done", "at": at(-3, 12)})
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": done.ID, "expected_version": 1})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Tomorrow", "at": at(1, 9)})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "No time"})
	execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Agent's", "subject": "agent", "at": at(0, 10)})

	d := execute[engine.Day](t, s, "day_get", map[string]any{})
	var titles []string
	for _, e := range d.Entries {
		titles = append(titles, e.Item.Title)
	}
	want := []string{"Standup", "Call Sam", "File taxes"} // today by time, then the overdue ones
	if len(titles) != len(want) {
		t.Fatalf("entries %v, want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("entries %v, want %v", titles, want)
		}
	}
	if !d.Entries[2].Overdue || d.Entries[0].Overdue {
		t.Fatalf("overdue flags: %+v", d.Entries)
	}
	// Another day has no carried-over tasks.
	other := execute[engine.Day](t, s, "day_get", map[string]any{"day": today.AddDate(0, 0, 1).Format("2006-01-02")})
	if len(other.Entries) != 1 || other.Entries[0].Item.Title != "Tomorrow" {
		t.Fatalf("tomorrow: %+v", other.Entries)
	}
}

func TestARepeatingTaskIsDoneOneOccurrenceAtATime(t *testing.T) {
	s := integrationService(t)
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3).Add(8 * time.Hour) // three days ago, 08:00 UTC
	task := execute[engine.Item](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Drink water", "at": start.Format(time.RFC3339), "recurrence": map[string]any{"freq": "daily"}})

	// Every day since shows its own occurrence; none is done.
	for back := 3; back >= 0; back-- {
		day := start.AddDate(0, 0, 3-back).Format("2006-01-02")
		d := execute[engine.Day](t, s, "day_get", map[string]any{"day": day})
		if len(d.Entries) != 1 || d.Entries[0].State != "open" {
			t.Fatalf("%s: %+v", day, d.Entries)
		}
	}
	// Doing yesterday's occurrence clears only that one.
	yesterday := start.AddDate(0, 0, 2)
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "at": yesterday.Format(time.RFC3339)})
	d := execute[engine.Day](t, s, "day_get", map[string]any{"day": yesterday.Format("2006-01-02")})
	if d.Entries[0].State != "done" {
		t.Fatalf("yesterday: %+v", d.Entries[0])
	}
	d = execute[engine.Day](t, s, "day_get", map[string]any{"day": start.AddDate(0, 0, 1).Format("2006-01-02")})
	if d.Entries[0].State != "open" {
		t.Fatalf("the day before is untouched: %+v", d.Entries[0])
	}
	// An occurrence can be reopened.
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "at": yesterday.Format(time.RFC3339), "done": false})
	d = execute[engine.Day](t, s, "day_get", map[string]any{"day": yesterday.Format("2006-01-02")})
	if d.Entries[0].State != "open" {
		t.Fatalf("reopened occurrence: %+v", d.Entries[0])
	}
	execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "at": yesterday.Format(time.RFC3339)})
	// Completing with no time clears the occurrence now due, and the task itself carries on.
	again := execute[engine.Item](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1})
	if again.DoneAt != nil || again.Version != 1 {
		t.Fatalf("a repeating task is never done as a whole: %+v", again)
	}
}

func TestABatchLandsWholeOrNotAtAll(t *testing.T) {
	s := integrationService(t)
	gid, tid := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	plan := map[string]any{"operations": []any{
		map[string]any{"operation": "goals_create", "input": map[string]any{"id": gid, "title": "Lisbon trip", "ends_on": "2026-06-10"}},
		map[string]any{"operation": "tasks_create", "input": map[string]any{"id": tid, "title": "Book flights", "goal_id": gid, "at": ts(24 * time.Hour)}},
		map[string]any{"operation": "calendar_events_create", "input": map[string]any{"title": "Flight", "goal_id": gid, "at": ts(72 * time.Hour)}},
	}}
	dry := execute[engine.BatchResult](t, s, "batch_dry_run", plan)
	if !dry.DryRun || len(dry.Steps) != 3 {
		t.Fatalf("dry run: %+v", dry)
	}
	if got := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{}); got.Total != 0 {
		t.Fatalf("a dry run created something: %d", got.Total)
	}
	k := key()
	apply := map[string]any{"idempotency_key": k, "operations": plan["operations"]}
	res := execute[engine.BatchResult](t, s, "batch_apply", apply)
	if len(res.Steps) != 3 {
		t.Fatalf("applied: %+v", res)
	}
	if got := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": gid}); got.Progress.Total != 1 {
		t.Fatalf("the task links to the goal chosen in the same batch: %+v", got.Progress)
	}
	// One bad step lands nothing.
	bad := map[string]any{"idempotency_key": key(), "operations": []any{
		map[string]any{"operation": "goals_create", "input": map[string]any{"title": "Half"}},
		map[string]any{"operation": "tasks_create", "input": map[string]any{"title": ""}},
	}}
	errorCode(t, s, "batch_apply", bad, "invalid_input")
	if got := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{}); got.Total != 1 {
		t.Fatalf("a failed batch left something behind: %d", got.Total)
	}
	// Undo removes what the batch made.
	undo := execute[engine.BatchUndoResult](t, s, "batch_undo", map[string]any{"idempotency_key": key(), "batch_key": k})
	if len(undo.Steps) != 3 {
		t.Fatalf("undo: %+v", undo)
	}
	if got := execute[engine.Page[engine.Goal]](t, s, "goals_list", map[string]any{}); got.Total != 0 {
		t.Fatalf("goals after undo: %d", got.Total)
	}
	if got := execute[engine.Page[engine.Item]](t, s, "tasks_list", map[string]any{}); got.Total != 0 {
		t.Fatalf("tasks after undo: %d", got.Total)
	}
}
