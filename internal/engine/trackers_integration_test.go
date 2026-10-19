package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

// runView is a goal's view of a run toward 5 km, in the phone catalog.
func runView(goal string) map[string]any {
	return map[string]any{
		"idempotency_key": key(), "subject_kind": "goal", "subject_id": goal, "catalog_id": engine.TrackerCatalogKit,
		"metrics": []map[string]any{{"id": "distance", "label": "Distance", "unit": "km", "minimum": 0, "target": 5}},
		"blocks": []map[string]any{
			{"id": "distance", "kind": "series", "title": "Distance", "metric": "distance"},
			{"id": "milestones", "kind": "steps", "title": "Next milestones", "source": "goal_steps"},
			{"id": "training", "kind": "text", "title": "Supporting routines", "body": "Short runs on weekdays."},
			{"id": "log_distance", "kind": "log", "title": "Record a run", "metric": "distance"},
		},
	}
}

func TestViewStateOpensATracker(t *testing.T) {
	s := integrationService(t)
	catalog := execute[engine.TrackerCatalogResult](t, s, "tracker_catalog_get", map[string]any{})
	if len(catalog.Catalogs) != 1 || len(catalog.Catalogs[0].Blocks) != 9 || len(catalog.Catalogs[0].Functions) < 15 {
		t.Fatalf("catalog: %+v", catalog)
	}
	errorCode(t, s, "tracker_catalog_get", map[string]any{"catalog_id": "other-app.ios.view-kit.v1"}, "invalid_input")
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K", "steps": []map[string]any{{"title": "Run 1 km"}}})
	none := execute[engine.ViewState](t, s, "views_state", map[string]any{"goal_id": g.ID, "catalog_id": engine.TrackerCatalogKit})
	if none.View != nil || none.Measurements == nil {
		t.Fatalf("a goal with no view says so, with a list rather than null: %+v", none)
	}
	v := execute[engine.View](t, s, "views_create", runView(g.ID))
	state := execute[engine.ViewState](t, s, "views_state", map[string]any{"goal_id": g.ID, "catalog_id": engine.TrackerCatalogKit})
	if state.View == nil || state.View.ID != v.ID || len(state.Measurements) != 0 || len(state.View.Metrics) != 1 {
		t.Fatalf("state: %+v", state)
	}
	errorCode(t, s, "views_state", map[string]any{"goal_id": g.ID, "catalog_id": "nope"}, "invalid_input")
	errorCode(t, s, "views_state", map[string]any{"goal_id": "00000000-0000-4000-8000-000000000000", "catalog_id": engine.TrackerCatalogKit}, "not_found")
}

func TestMeasurementRules(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run"})
	execute[engine.View](t, s, "views_create", runView(g.ID))
	input := map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "distance", "value": 2.5}
	m := execute[engine.GoalMeasurement](t, s, "goal_measurements_record", input)
	if replay := execute[engine.GoalMeasurement](t, s, "goal_measurements_record", input); replay.ID != m.ID {
		t.Fatal("measurement retry duplicated the observation")
	}
	record := func(extra map[string]any) map[string]any {
		in := map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "distance", "value": 2}
		for k, v := range extra {
			in[k] = v
		}
		return in
	}
	errorCode(t, s, "goal_measurements_record", record(map[string]any{"value": -1}), "invalid_input")
	errorCode(t, s, "goal_measurements_record", record(map[string]any{"metric_id": "missing"}), "invalid_input")
	errorCode(t, s, "goal_measurements_record", record(map[string]any{"measured_at": time.Now().Add(time.Hour).Format(time.RFC3339)}), "invalid_input")
	updated := execute[engine.GoalMeasurement](t, s, "goal_measurements_update", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 1, "value": 3})
	if !updated.MeasuredAt.Equal(m.MeasuredAt) || updated.Version != 2 {
		t.Fatal("correction lost observation time or version")
	}
	errorCode(t, s, "goal_measurements_update", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 1, "value": 4}, "conflict")
	execute[engine.GoalMeasurement](t, s, "goal_measurements_delete", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 2})
	state := execute[engine.ViewState](t, s, "views_state", map[string]any{"goal_id": g.ID, "catalog_id": engine.TrackerCatalogKit})
	if len(state.Measurements) != 0 {
		t.Fatal("removed measurement is still charted")
	}
	history := execute[engine.Page[engine.Change]](t, s, "history_list", map[string]any{"entity_type": "goal_measurement", "id": m.ID})
	if history.Total != 3 {
		t.Fatalf("audit history: %d", history.Total)
	}
}

func TestWriteTrackerFixtures(t *testing.T) {
	s := integrationService(t)
	dir := os.Getenv("LIFE_WRITE_TRACKER_FIXTURES")
	save := func(name, op string, input any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(input)
		out, e := s.Execute(context.Background(), op, raw)
		if e != nil {
			t.Fatal(e)
		}
		if dir != "" {
			_ = os.MkdirAll(dir, 0755)
			if e = os.WriteFile(filepath.Join(dir, name+".json"), append(out, '\n'), 0644); e != nil {
				t.Fatal(e)
			}
		}
		var result map[string]any
		_ = json.Unmarshal(out, &result)
		return result
	}
	save("tracker_catalogs", "tracker_catalog_get", map[string]any{})
	g := save("goal_for_view", "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K", "steps": []map[string]any{{"title": "Run 1 km"}}})
	gid := g["id"].(string)
	view := runView(gid)
	view["metrics"] = append(view["metrics"].([]map[string]any), map[string]any{"id": "weight", "label": "Weight", "unit": "kg", "source": map[string]any{"provider": "health", "series": "body_weight"}})
	view["blocks"] = append(view["blocks"].([]map[string]any), map[string]any{"id": "weight", "kind": "series", "title": "Weight", "metric": "weight"}, map[string]any{"id": "plan", "kind": "link", "title": "The plan", "url": "https://example.com/plan"})
	save("view", "views_create", view)
	save("goal_measurement", "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": gid, "metric_id": "distance", "value": 2.5})
	save("view_state", "views_state", map[string]any{"goal_id": gid, "catalog_id": engine.TrackerCatalogKit})
}
