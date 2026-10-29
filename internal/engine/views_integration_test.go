package engine_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

func viewOf(kind, subject string, blocks ...map[string]any) map[string]any {
	return map[string]any{"idempotency_key": key(), "subject_kind": kind, "subject_id": subject, "catalog_id": engine.TrackerCatalogKit,
		"metrics": []map[string]any{{"id": "distance", "label": "Distance", "unit": "km", "target": 5}},
		"pages":   []map[string]any{{"id": "main", "sections": []map[string]any{{"id": "s", "title": "Progress", "blocks": blocks}}}}}
}

var logBlock = map[string]any{"id": "log", "kind": "log", "metric": "distance"}
var todoBlock = map[string]any{"id": "todo", "kind": "tasks", "title": "Tasks"}

// A view is its own construct: it can be of a goal, a task or an event, and a client opens it by subject and catalog.
func TestAViewCanBeOfAGoalATaskOrAnEvent(t *testing.T) {
	s := integrationService(t)
	goal := execute[engine.Intention](t, s, "intentions_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K"})
	task := execute[engine.Task](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Long run", "at": ts(24 * time.Hour)})
	event := execute[engine.Task](t, s, "calendar_events_create", map[string]any{"idempotency_key": key(), "title": "Race day", "at": ts(48 * time.Hour)})

	for kind, id := range map[string]string{"goal": goal.ID, "task": task.ID, "event": event.ID} {
		v := execute[engine.View](t, s, "views_create", viewOf(kind, id, todoBlock, logBlock))
		if v.SubjectKind != kind || v.SubjectID != id || v.Version != 1 {
			t.Fatalf("%s view: %+v", kind, v)
		}
		state := execute[engine.ViewState](t, s, "views_state", map[string]any{"subject_kind": kind, "subject_id": id, "catalog_id": engine.TrackerCatalogKit})
		if state.View == nil || state.View.ID != v.ID || len(state.Measurements) != 0 {
			t.Fatalf("%s state: %+v", kind, state)
		}
	}
	// One live view per subject and catalog; an unknown subject, kind or catalog is refused; a subject that is not there too.
	errorCode(t, s, "views_create", viewOf("goal", goal.ID, todoBlock), "conflict")
	errorCode(t, s, "views_create", viewOf("routine", goal.ID, todoBlock), "invalid_input")
	errorCode(t, s, "views_create", viewOf("goal", uuid.NewString(), todoBlock), "invalid_input")
	errorCode(t, s, "views_create", viewOf("task", goal.ID, todoBlock), "invalid_input") // a goal's id is not a task
	bad := viewOf("goal", uuid.NewString(), todoBlock)
	bad["catalog_id"] = "nope"
	errorCode(t, s, "views_create", bad, "invalid_input")
	errorCode(t, s, "views_state", map[string]any{"subject_kind": "task", "subject_id": uuid.NewString(), "catalog_id": engine.TrackerCatalogKit}, "invalid_input")
}

func TestObservationsAreKeptPerSubjectAndMetric(t *testing.T) {
	s := integrationService(t)
	goal := execute[engine.Intention](t, s, "intentions_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K"})
	v := execute[engine.View](t, s, "views_create", viewOf("goal", goal.ID, logBlock))
	rec := map[string]any{"idempotency_key": key(), "subject_kind": "intention", "subject_id": goal.ID, "metric_id": "distance", "value": 2.5}
	m := execute[engine.Measurement](t, s, "measurements_record", rec)
	if m.Unit != "km" || m.Value != 2.5 || m.SubjectKind != "goal" || m.SubjectID != goal.ID {
		t.Fatalf("recorded: %+v", m)
	}
	rec["idempotency_key"] = key()
	rec["metric_id"] = "ghost" // a metric no view of the subject defines
	errorCode(t, s, "measurements_record", rec, "invalid_input")
	rec["metric_id"], rec["value"] = "distance", 1e9
	rec["idempotency_key"] = key()
	rec["measured_at"] = ts(2 * time.Hour) // the future
	errorCode(t, s, "measurements_record", rec, "invalid_input")

	upd := execute[engine.Measurement](t, s, "measurements_update", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 1, "value": 3.0})
	if upd.Value != 3 || upd.Version != 2 {
		t.Fatalf("updated: %+v", upd)
	}
	// A metric that has observations stays, in its unit.
	changed := map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": 1, "pages": viewOf("goal", goal.ID, logBlock)["pages"],
		"metrics": []map[string]any{{"id": "distance", "label": "Distance", "unit": "mi"}}}
	errorCode(t, s, "views_update", changed, "conflict")
	changed["idempotency_key"] = key()
	changed["metrics"] = []map[string]any{}
	changed["pages"] = viewOf("goal", goal.ID, map[string]any{"id": "t", "kind": "text", "body": "x"})["pages"]
	errorCode(t, s, "views_update", changed, "conflict")
	state := execute[engine.ViewState](t, s, "views_state", map[string]any{"subject_kind": "intention", "subject_id": goal.ID, "catalog_id": engine.TrackerCatalogKit})
	if len(state.Measurements) != 1 || state.Measurements[0].Value != 3 {
		t.Fatalf("state: %+v", state.Measurements)
	}
	execute[engine.Measurement](t, s, "measurements_delete", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 2})
	if got := execute[engine.ViewState](t, s, "views_state", map[string]any{"subject_kind": "intention", "subject_id": goal.ID, "catalog_id": engine.TrackerCatalogKit}); len(got.Measurements) != 0 {
		t.Fatalf("deleted still shown: %+v", got.Measurements)
	}
	execute[engine.View](t, s, "views_delete", map[string]any{"idempotency_key": key(), "id": v.ID, "expected_version": 1})
	if got := execute[engine.ViewState](t, s, "views_state", map[string]any{"subject_kind": "intention", "subject_id": goal.ID, "catalog_id": engine.TrackerCatalogKit}); got.View != nil {
		t.Fatalf("a removed view: %+v", got.View)
	}
}

// A composed use case lands whole: the goal, its tasks and its view, all or nothing, and comes away as one.
func TestAViewIsPartOfABatchAndUndoesWithIt(t *testing.T) {
	s := integrationService(t)
	gid := uuid.NewString()
	ops := []any{
		map[string]any{"operation": "intentions_create", "input": map[string]any{"id": gid, "title": "Lisbon trip"}},
		map[string]any{"operation": "tasks_create", "input": map[string]any{"title": "Book flights", "goal_id": gid}},
		map[string]any{"operation": "views_create", "input": viewOf("goal", gid, todoBlock)},
	}
	k := key()
	execute[engine.BatchResult](t, s, "batch_apply", map[string]any{"idempotency_key": k, "operations": ops})
	if got := execute[engine.ViewState](t, s, "views_state", map[string]any{"subject_kind": "intention", "subject_id": gid, "catalog_id": engine.TrackerCatalogKit}); got.View == nil {
		t.Fatal("the batch's view is missing")
	}
	undo := execute[engine.BatchUndoResult](t, s, "batch_undo", map[string]any{"idempotency_key": key(), "batch_key": k})
	if len(undo.Steps) != 3 {
		t.Fatalf("undo: %+v", undo)
	}
}

func TestTheKitNamesTasksAndEventsNotRoutinesOrReminders(t *testing.T) {
	s := integrationService(t)
	cat := execute[engine.TrackerCatalogResult](t, s, "tracker_catalog_get", map[string]any{})
	kinds := map[string]bool{}
	for _, b := range cat.Catalogs[0].Blocks {
		kinds[b.Kind] = true
	}
	if !kinds["tasks"] || !kinds["events"] || kinds["routines"] || kinds["reminders"] {
		t.Fatalf("kit blocks: %v", kinds)
	}
	var example map[string]any
	if err := json.Unmarshal(cat.Catalogs[0].ExampleView, &example); err != nil {
		t.Fatal(err)
	}
}

func TestAHabitsHistoryIsReadFromItsOccurrences(t *testing.T) {
	s := integrationService(t)
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -9).Add(8 * time.Hour)
	task := execute[engine.Task](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Stretch", "at": start.Format(time.RFC3339), "recurrence": map[string]any{"freq": "daily"}})
	for _, back := range []int{0, 1, 2, 4, 5} { // done today, yesterday, two days ago, then a gap, then two more
		at := start.AddDate(0, 0, 9-back)
		execute[engine.Task](t, s, "tasks_complete", map[string]any{"idempotency_key": key(), "id": task.ID, "expected_version": 1, "at": at.Format(time.RFC3339)})
	}
	h := execute[engine.TaskHistory](t, s, "tasks_history", map[string]any{"id": task.ID, "days": 10})
	if len(h.Days) != 10 || h.Last7.Scheduled != 7 || h.Last7.Done != 5 || h.Streak != 3 || h.BestStreak != 3 {
		t.Fatalf("history: last7 %+v streak %d best %d", h.Last7, h.Streak, h.BestStreak)
	}
	one := execute[engine.Task](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Once"})
	errorCode(t, s, "tasks_history", map[string]any{"id": one.ID}, "invalid_input")
}
