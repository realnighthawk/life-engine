package engine_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

const iosCatalog = "mission-control.ios.view-kit.v1"

func weightView(goal string) map[string]any {
	return map[string]any{
		"subject_kind": "goal", "subject_id": goal, "catalog_id": iosCatalog,
		"metrics": []map[string]any{
			{"id": "weight", "label": "Weight", "unit": "kg", "target": 77, "source": map[string]any{"provider": "health", "series": "body_weight"}},
			{"id": "waist", "label": "Waist", "unit": "cm"},
		},
		"blocks": []map[string]any{
			{"id": "trend", "kind": "series", "title": "Weight", "metric": "weight"},
			{"id": "log", "kind": "log", "title": "Waist", "metric": "waist"},
			{"id": "steps", "kind": "steps", "title": "Steps", "source": "goal_steps"},
			{"id": "habits", "kind": "text", "title": "Habits", "body": "Walk after lunch."},
			{"id": "guide", "kind": "link", "title": "Plan", "url": "https://example.com/plan"},
		},
	}
}

func TestViewRoundTrip(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Lose 5 kg"})
	in := weightView(g.ID)
	in["idempotency_key"] = key()
	v := execute[engine.View](t, s, "views_create", in)
	if v.SubjectID != g.ID || len(v.Blocks) != 5 || v.Version != 1 {
		t.Fatalf("created: %+v", v)
	}
	if v.Metrics[0].Source == nil || v.Metrics[0].Source.Provider != "health" || v.Metrics[1].Source != nil {
		t.Fatalf("a bound metric keeps its source and a manual one has none: %+v", v.Metrics)
	}
	if got := execute[engine.View](t, s, "views_get", map[string]any{"id": v.ID}); got.ID != v.ID || got.Blocks[4].URL != "https://example.com/plan" {
		t.Fatalf("get: %+v", got)
	}
	list := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": g.ID})
	if len(list.Items) != 1 || list.Items[0].ID != v.ID {
		t.Fatalf("list: %+v", list)
	}
	if none := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": g.ID, "catalog_id": "mission-control.ipados.view-kit.v1"}); len(none.Items) != 0 {
		t.Fatalf("a different catalog has no view: %+v", none)
	}
	in["idempotency_key"] = key()
	errorCode(t, s, "views_create", in, "conflict") // one live view per subject and catalog
}

func TestViewIsLastStepOfAPlan(t *testing.T) {
	s := integrationService(t)
	routine, goal := uuid.NewString(), uuid.NewString()
	steps := plan(routine, goal)
	view := weightView(goal)
	steps = append(steps, op("views_create", view))
	res := execute[engine.BatchResult](t, s, "batch_dry_run", map[string]any{"operations": steps})
	if len(res.Steps) != 4 {
		t.Fatalf("dry run: %+v", res)
	}
	if list := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": goal}); len(list.Items) != 0 {
		t.Fatalf("a dry run must create nothing: %+v", list)
	}
	execute[engine.BatchResult](t, s, "batch_apply", map[string]any{"idempotency_key": key(), "operations": steps})
	if list := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": goal}); len(list.Items) != 1 {
		t.Fatalf("the plan's view: %+v", list)
	}
	// A view of a goal nothing creates is refused, and the step is named.
	orphan := []map[string]any{op("views_create", weightView(uuid.NewString()))}
	_, e := s.Execute(t.Context(), "batch_dry_run", mustJSON(t, map[string]any{"operations": orphan}))
	if pe := engine.PublicError(e); e == nil || !strings.HasPrefix(pe.Message, "operations[0] views_create:") {
		t.Fatalf("wanted a refusal naming the step, got %v", e)
	}
}

func TestViewValidation(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Read more"})
	bad := map[string]func(map[string]any){
		"unknown block kind":                           func(v map[string]any) { v["blocks"].([]map[string]any)[0]["kind"] = "html" },
		"measurement block without a metric":           func(v map[string]any) { delete(v["blocks"].([]map[string]any)[0], "metric") },
		"measurement block naming an undefined metric": func(v map[string]any) { v["blocks"].([]map[string]any)[0]["metric"] = "ghost" },
		"link without a url":                           func(v map[string]any) { delete(v["blocks"].([]map[string]any)[4], "url") },
		"link that is not http(s)":                     func(v map[string]any) { v["blocks"].([]map[string]any)[4]["url"] = "javascript:alert(1)" },
		"steps carrying a url":                         func(v map[string]any) { v["blocks"].([]map[string]any)[2]["url"] = "https://example.com" },
		"duplicate block id":                           func(v map[string]any) { v["blocks"].([]map[string]any)[1]["id"] = "trend" },
		"no blocks":                                    func(v map[string]any) { v["blocks"] = []map[string]any{} },
		"unknown catalog":                              func(v map[string]any) { v["catalog_id"] = "nope" },
		"bad source identifier": func(v map[string]any) {
			v["metrics"].([]map[string]any)[0]["source"] = map[string]any{"provider": "Health!", "series": "w"}
		},
		"subject kind":            func(v map[string]any) { v["subject_kind"] = "routine" },
		"subject that is missing": func(v map[string]any) { v["subject_id"] = uuid.NewString() },
	}
	for name, mutate := range bad {
		in := weightView(g.ID)
		in["idempotency_key"] = key()
		mutate(in)
		raw := mustJSON(t, in)
		if _, e := s.Execute(t.Context(), "views_create", raw); e == nil || engine.PublicError(e).Code != "invalid_input" {
			t.Errorf("%s: wanted invalid_input, got %v", name, e)
		}
	}
}
