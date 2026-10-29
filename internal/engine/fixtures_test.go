package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

// TestWriteClientFixtures records real engine responses for the client apps to decode in their own tests (the iOS app
// keeps a copy under Tests/Fixtures/life). It writes nothing unless LIFE_WRITE_FIXTURES names a directory, so it is a
// no-op in CI, but it always runs the scenario and checks every response is a JSON object, which is the contract.
//
//	LIFE_WRITE_FIXTURES=$PWD/fixtures TEST_DATABASE_URL=... go test ./internal/engine -run TestWriteClientFixtures
func TestWriteClientFixtures(t *testing.T) {
	s := integrationService(t)
	dir := os.Getenv("LIFE_WRITE_FIXTURES")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name, op string, body any) map[string]any {
		t.Helper()
		in, _ := json.Marshal(body)
		raw, err := s.Execute(context.Background(), op, in)
		if err != nil {
			t.Fatalf("%s (%s): %v", name, op, engine.PublicError(err))
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			t.Fatalf("%s: a response must be a JSON object: %v", name, err)
		}
		if dir != "" {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, raw, "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			if err := os.WriteFile(filepath.Join(dir, name+".json"), pretty.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return obj
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	at := func(h int) string { return today.Add(time.Duration(h) * time.Hour).Format(time.RFC3339) }

	call("settings_get", "settings_get", map[string]any{})
	goal := call("goal", "intentions_create", map[string]any{"idempotency_key": key(), "title": "Lisbon trip", "ends_on": "2027-06-10", "notes": "take the coolers"})
	task := call("task", "tasks_create", map[string]any{"idempotency_key": key(), "title": "Book flights", "goal_id": goal["id"], "at": at(23), "minutes": 30, "alert_before_minutes": 15, "is_priority": true})
	repeating := call("task_repeating", "tasks_create", map[string]any{"idempotency_key": key(), "title": "Drink water", "at": at(8), "time_zone": "Europe/London", "recurrence": map[string]any{"freq": "daily"}, "alert_before_minutes": 0})
	call("event", "calendar_events_create", map[string]any{"idempotency_key": key(), "title": "Standup", "at": at(9), "minutes": 15})
	call("tasks_page", "tasks_list", map[string]any{})
	call("events_page", "calendar_events_list", map[string]any{})
	call("goal_with_progress", "intentions_get", map[string]any{"id": goal["id"]})
	call("goals_page", "intentions_list", map[string]any{})
	call("day", "day_get", map[string]any{})
	call("task_snoozed", "tasks_snooze", map[string]any{"idempotency_key": key(), "id": task["id"], "expected_version": 1, "until": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)})
	call("task_done", "tasks_complete", map[string]any{"idempotency_key": key(), "id": task["id"], "expected_version": 2})
	call("tracker_catalogs", "tracker_catalog_get", map[string]any{})
	view := call("view", "views_create", map[string]any{"idempotency_key": key(), "subject_kind": "intention", "subject_id": goal["id"], "catalog_id": engine.TrackerCatalogKit,
		"metrics": []map[string]any{{"id": "distance", "label": "Distance", "unit": "km", "target": 5}},
		"pages": []map[string]any{{"id": "main", "sections": []map[string]any{{"id": "s", "title": "Progress", "blocks": []map[string]any{
			{"id": "log", "kind": "log", "metric": "distance"}, {"id": "todo", "kind": "tasks", "title": "Tasks"}}}}}}})
	_ = view
	call("measurement", "measurements_record", map[string]any{"idempotency_key": key(), "subject_kind": "intention", "subject_id": goal["id"], "metric_id": "distance", "value": 2.5})
	call("view_state", "views_state", map[string]any{"subject_kind": "intention", "subject_id": goal["id"], "catalog_id": engine.TrackerCatalogKit})
	call("task_history", "tasks_history", map[string]any{"id": repeating["id"], "days": 14})
	call("events_pending", "events_pending", map[string]any{})
	call("history", "history_list", map[string]any{"entity_type": "item", "id": task["id"]})
	call("batch_dry_run", "batch_dry_run", map[string]any{"operations": []any{map[string]any{"operation": "intentions_create", "input": map[string]any{"title": "Dinner party"}}}})
	for name, body := range map[string]map[string]any{
		"error_stale_version": {"idempotency_key": key(), "id": task["id"], "expected_version": 1, "title": "x"},
	} {
		in, _ := json.Marshal(body)
		_, err := s.Execute(context.Background(), "tasks_update", in)
		if err == nil {
			t.Fatalf("%s: wanted an error", name)
		}
		if dir != "" {
			out, _ := json.MarshalIndent(map[string]any{"error": engine.PublicError(err)}, "", "  ")
			if e := os.WriteFile(filepath.Join(dir, name+".json"), append(out, '\n'), 0o644); e != nil {
				t.Fatal(e)
			}
		}
	}
}
