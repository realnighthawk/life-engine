package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func trackerDefinition() engine.TrackerDefinition {
	min, target := 0.0, 5.0
	sections := []engine.TrackerSection{{ID: "distance", Type: "metric_trend", Title: "Distance", MetricID: "distance"}, {ID: "milestones", Type: "milestones", Title: "Next milestones"}, {ID: "training", Type: "routine_consistency", Title: "Supporting routines"}, {ID: "log_distance", Type: "check_in", Title: "Record a run", MetricID: "distance"}}
	return engine.TrackerDefinition{Metrics: []engine.TrackerMetric{{ID: "distance", Label: "Distance", Unit: "km", Minimum: &min, Target: &target}}, Views: []engine.TrackerView{{CatalogID: engine.TrackerCatalogIOS, Sections: sections}, {CatalogID: engine.TrackerCatalogIPad, Sections: sections}}}
}
func TestTrackerCreateAndProposal(t *testing.T) {
	s := integrationService(t)
	d := trackerDefinition()
	catalog := execute[engine.TrackerCatalogResult](t, s, "tracker_catalog_get", map[string]any{})
	if len(catalog.Catalogs) != 2 || len(catalog.Catalogs[0].Modules) != 4 {
		t.Fatalf("catalog: %+v", catalog)
	}
	errorCode(t, s, "tracker_catalog_get", map[string]any{"catalog_id": "other-app.ios.goal-tracker.v1"}, "invalid_input")
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K", "tracker": d, "steps": []map[string]any{{"title": "Run 1 km"}}})
	if g.Tracker == nil || g.TrackerRevision != 1 {
		t.Fatal("tracker must be stored with the new goal")
	}
	state := execute[engine.TrackerState](t, s, "goals_tracker_get", map[string]any{"goal_id": g.ID, "catalog_id": engine.TrackerCatalogIOS})
	if state.View == nil || len(state.Measurements) != 0 || len(state.Metrics) != 1 {
		t.Fatalf("state: %+v", state)
	}
	d.Views[0].Sections[0].Title = "Running progress"
	missingPlatform := trackerDefinition()
	missingPlatform.Views = missingPlatform.Views[:1]
	errorCode(t, s, "goals_propose_tracker", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": g.Version, "definition": missingPlatform, "reason": "Drop iPad"}, "conflict")
	p := execute[engine.TrackerProposal](t, s, "goals_propose_tracker", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": g.Version, "definition": d, "reason": "Make your outcome clearer"})
	unchanged := execute[engine.Goal](t, s, "goals_get", map[string]any{"id": g.ID})
	if unchanged.TrackerRevision != 1 || unchanged.Tracker.Views[0].Sections[0].Title == "Running progress" {
		t.Fatal("proposal changed the live tracker")
	}
	applied := execute[engine.Goal](t, s, "goals_tracker_apply", map[string]any{"idempotency_key": key(), "id": p.ID, "expected_version": p.Version})
	if applied.TrackerRevision != 2 || applied.Version != g.Version+1 || applied.Tracker.Views[0].Sections[0].Title != "Running progress" {
		t.Fatalf("apply: %+v", applied)
	}
	errorCode(t, s, "goals_tracker_apply", map[string]any{"idempotency_key": key(), "id": p.ID, "expected_version": p.Version}, "conflict")
	stale := execute[engine.TrackerProposal](t, s, "goals_propose_tracker", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": applied.Version, "definition": d, "reason": "Another presentation"})
	execute[engine.Goal](t, s, "goals_update", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": applied.Version, "title": "Run a 10K"})
	errorCode(t, s, "goals_tracker_apply", map[string]any{"idempotency_key": key(), "id": stale.ID, "expected_version": 1}, "conflict")
	execute[engine.TrackerProposal](t, s, "goals_tracker_discard", map[string]any{"idempotency_key": key(), "id": stale.ID, "expected_version": 1})
	milestonesOnly := engine.TrackerDefinition{Views: []engine.TrackerView{{CatalogID: engine.TrackerCatalogIOS, Sections: []engine.TrackerSection{{ID: "milestones", Type: "milestones", Title: "Milestones"}}}}}
	simple := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Ship a draft", "tracker": milestonesOnly})
	simpleState := execute[engine.TrackerState](t, s, "goals_tracker_get", map[string]any{"goal_id": simple.ID, "catalog_id": engine.TrackerCatalogIOS})
	if simpleState.Metrics == nil || simpleState.View == nil {
		t.Fatal("empty metric array must remain schema compatible")
	}
	legacy := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Legacy client goal"})
	noView := execute[engine.TrackerState](t, s, "goals_tracker_get", map[string]any{"goal_id": legacy.ID, "catalog_id": engine.TrackerCatalogIOS})
	if noView.View != nil || noView.TrackerRevision != 0 {
		t.Fatal("legacy goal needs explicit fallback, not an invented view")
	}
	d.Views = d.Views[:1]
	single := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Phone only", "tracker": d})
	tablet := execute[engine.TrackerState](t, s, "goals_tracker_get", map[string]any{"goal_id": single.ID, "catalog_id": engine.TrackerCatalogIPad})
	if tablet.View != nil {
		t.Fatal("do not substitute a view for another platform")
	}
}
func TestTrackerValidationAndMeasurements(t *testing.T) {
	s, pool := integrationEnv(t)
	d := trackerDefinition()
	bad := trackerDefinition()
	bad.Views[0].Sections[0].Type = "webview"
	errorCode(t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Invalid", "tracker": bad}, "invalid_input")
	var count int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM life.goals").Scan(&count)
	if count != 0 {
		t.Fatal("failed tracker creation left a partial goal")
	}
	bad = trackerDefinition()
	bad.Views[0].Sections[0].MetricID = "missing"
	errorCode(t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Invalid", "tracker": bad}, "invalid_input")
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run", "tracker": d})
	input := map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "distance", "expected_tracker_revision": 1, "value": 2.5}
	m := execute[engine.GoalMeasurement](t, s, "goal_measurements_record", input)
	replay := execute[engine.GoalMeasurement](t, s, "goal_measurements_record", input)
	if replay.ID != m.ID {
		t.Fatal("measurement retry duplicated the observation")
	}
	errorCode(t, s, "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "distance", "expected_tracker_revision": 1, "value": -1}, "invalid_input")
	errorCode(t, s, "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "missing", "expected_tracker_revision": 1, "value": 2}, "invalid_input")
	errorCode(t, s, "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "distance", "expected_tracker_revision": 2, "value": 2}, "conflict")
	errorCode(t, s, "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": g.ID, "metric_id": "distance", "expected_tracker_revision": 1, "value": 2, "measured_at": time.Now().Add(time.Hour).Format(time.RFC3339)}, "invalid_input")
	updated := execute[engine.GoalMeasurement](t, s, "goal_measurements_update", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 1, "value": 3})
	if !updated.MeasuredAt.Equal(m.MeasuredAt) || updated.Version != 2 {
		t.Fatal("correction lost observation time or version")
	}
	d.Metrics[0].Unit = "miles"
	errorCode(t, s, "goals_propose_tracker", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": g.Version, "definition": d, "reason": "Change unit"}, "conflict")
	errorCode(t, s, "goal_measurements_update", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 1, "value": 4}, "conflict")
	execute[engine.GoalMeasurement](t, s, "goal_measurements_delete", map[string]any{"idempotency_key": key(), "id": m.ID, "expected_version": 2})
	state := execute[engine.TrackerState](t, s, "goals_tracker_get", map[string]any{"goal_id": g.ID, "catalog_id": engine.TrackerCatalogIPad})
	if len(state.Measurements) != 0 {
		t.Fatal("removed measurement is still charted")
	}
	history := execute[engine.Page[engine.Change]](t, s, "history_list", map[string]any{"entity_type": "goal_measurement", "id": m.ID})
	if history.Total != 3 {
		t.Fatalf("audit history: %d", history.Total)
	}
}
func TestConcurrentTrackerAcceptance(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run", "tracker": trackerDefinition()})
	proposals := []engine.TrackerProposal{}
	for range 2 {
		proposals = append(proposals, execute[engine.TrackerProposal](t, s, "goals_propose_tracker", map[string]any{"idempotency_key": key(), "id": g.ID, "expected_version": 1, "definition": trackerDefinition(), "reason": "Revise"}))
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, p := range proposals {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"idempotency_key": key(), "id": p.ID, "expected_version": 1})
			_, e := s.Execute(context.Background(), "goals_tracker_apply", raw)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if engine.PublicError(e).Code != "conflict" {
			t.Fatalf("unexpected concurrent error: %v", e)
		}
	}
	if successes != 1 {
		t.Fatalf("accepted %d incompatible proposals", successes)
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
	g := save("goal_tracker", "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K", "tracker": trackerDefinition(), "steps": []map[string]any{{"title": "Run 1 km"}}})
	gid := g["id"].(string)
	save("goal_measurement", "goal_measurements_record", map[string]any{"idempotency_key": key(), "goal_id": gid, "metric_id": "distance", "expected_tracker_revision": 1, "value": 2.5})
	save("tracker_proposal", "goals_propose_tracker", map[string]any{"idempotency_key": key(), "id": gid, "expected_version": 1, "definition": trackerDefinition(), "reason": "Keep outcome and training progress separate"})
	save("tracker_state", "goals_tracker_get", map[string]any{"goal_id": gid, "catalog_id": engine.TrackerCatalogIOS})
}
