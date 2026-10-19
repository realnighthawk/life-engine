package engine_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

const kitCatalog = engine.TrackerCatalogKit

// pagesOf puts blocks on one page in one section, the plainest valid structure.
func pagesOf(blocks []map[string]any) []map[string]any {
	return []map[string]any{{"id": "main", "sections": []map[string]any{{"id": "all", "title": "Overview", "blocks": blocks}}}}
}

// blocksOf is the blocks of a view made with pagesOf.
func blocksOf(v map[string]any) []map[string]any {
	return v["pages"].([]map[string]any)[0]["sections"].([]map[string]any)[0]["blocks"].([]map[string]any)
}

func weightView(goal string) map[string]any {
	return map[string]any{
		"subject_kind": "goal", "subject_id": goal, "catalog_id": kitCatalog,
		"metrics": []map[string]any{
			{"id": "weight", "label": "Weight", "unit": "kg", "target": 77, "source": map[string]any{"provider": "health", "series": "body_weight"}},
			{"id": "waist", "label": "Waist", "unit": "cm"},
		},
		"pages": pagesOf([]map[string]any{
			{"id": "trend", "kind": "series", "title": "Weight", "metric": "weight"},
			{"id": "log", "kind": "log", "title": "Waist", "metric": "waist"},
			{"id": "steps", "kind": "steps", "title": "Steps", "source": "goal_steps"},
			{"id": "habits", "kind": "text", "title": "Habits", "body": "Walk after lunch."},
			{"id": "guide", "kind": "link", "title": "Plan", "url": "https://example.com/plan"},
		}),
	}
}

func TestViewRoundTrip(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Lose 5 kg"})
	in := weightView(g.ID)
	in["idempotency_key"] = key()
	v := execute[engine.View](t, s, "views_create", in)
	if v.SubjectID != g.ID || len(v.Pages[0].Sections[0].Blocks) != 5 || v.Version != 1 {
		t.Fatalf("created: %+v", v)
	}
	if v.Metrics[0].Source == nil || v.Metrics[0].Source.Provider != "health" || v.Metrics[1].Source != nil {
		t.Fatalf("a bound metric keeps its source and a manual one has none: %+v", v.Metrics)
	}
	if got := execute[engine.View](t, s, "views_get", map[string]any{"id": v.ID}); got.ID != v.ID || got.Pages[0].Sections[0].Blocks[4].URL != "https://example.com/plan" {
		t.Fatalf("get: %+v", got)
	}
	list := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": g.ID})
	if len(list.Items) != 1 || list.Items[0].ID != v.ID {
		t.Fatalf("list: %+v", list)
	}
	if none := execute[engine.ViewList](t, s, "views_list", map[string]any{"subject_kind": "goal", "subject_id": g.ID, "catalog_id": "another-kit.v1"}); len(none.Items) != 0 {
		t.Fatalf("a different kit has no view: %+v", none)
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
		"unknown block kind":                           func(v map[string]any) { blocksOf(v)[0]["kind"] = "html" },
		"measurement block without a metric":           func(v map[string]any) { delete(blocksOf(v)[0], "metric") },
		"measurement block naming an undefined metric": func(v map[string]any) { blocksOf(v)[0]["metric"] = "ghost" },
		"link without a url":                           func(v map[string]any) { delete(blocksOf(v)[4], "url") },
		"link that is not http(s)":                     func(v map[string]any) { blocksOf(v)[4]["url"] = "javascript:alert(1)" },
		"steps carrying a url":                         func(v map[string]any) { blocksOf(v)[2]["url"] = "https://example.com" },
		"duplicate block id":                           func(v map[string]any) { blocksOf(v)[1]["id"] = "trend" },
		"no blocks":                                    func(v map[string]any) { v["pages"] = pagesOf([]map[string]any{}) },
		"no pages":                                     func(v map[string]any) { v["pages"] = []map[string]any{} },
		"two pages need titles": func(v map[string]any) {
			v["pages"] = append(v["pages"].([]map[string]any), map[string]any{"id": "second", "sections": []map[string]any{{"id": "more", "title": "More", "blocks": []map[string]any{{"id": "note", "kind": "text", "body": "x"}}}}})
		},
		"a section without a title": func(v map[string]any) {
			v["pages"].([]map[string]any)[0]["sections"].([]map[string]any)[0]["title"] = ""
		},
		"too many points": func(v map[string]any) { blocksOf(v)[3]["points"] = []string{"a", "b", "c", "d", "e", "f"} },
		"unknown catalog": func(v map[string]any) { v["catalog_id"] = "nope" },
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

// The kit's examples go through the real operation: decoded, validated, stored and read back whole.
func TestExampleViewsAreCreatedThroughTheService(t *testing.T) {
	s := integrationService(t)
	for _, name := range []string{"dining_limit", "weight_target", "habit_days", "savings_deadline"} {
		raw, e := os.ReadFile("testdata/views/" + name + ".json")
		if e != nil {
			t.Fatal(e)
		}
		g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": name})
		in := map[string]any{}
		if e = json.Unmarshal(raw, &in); e != nil {
			t.Fatal(e)
		}
		in["subject_kind"], in["subject_id"], in["catalog_id"], in["idempotency_key"] = "goal", g.ID, kitCatalog, key()
		v := execute[engine.View](t, s, "views_create", in)
		got := execute[engine.View](t, s, "views_get", map[string]any{"id": v.ID})
		if len(got.Pages) != len(in["pages"].([]any)) || got.Pages[0].Sections[0].Blocks[0].Kind == "" {
			t.Errorf("%s: stored %+v", name, got.Pages)
		}
	}
}

// A view's reminders block shows the reminders linked to its goal, so the list can be filtered by that link.
func TestRemindersAreListedByTheRecordTheyAreLinkedTo(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K"})
	other := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Read more"})
	for _, c := range []struct{ title, goal string }{{"Stretch", g.ID}, {"Library card", other.ID}, {"Loose", ""}} {
		in := map[string]any{"idempotency_key": key(), "title": c.title}
		if c.goal != "" {
			in["link"] = map[string]any{"entity": "goal", "id": c.goal}
		}
		execute[engine.Reminder](t, s, "reminders_create", in)
	}
	got := execute[engine.Page[engine.Reminder]](t, s, "reminders_list", map[string]any{"link_entity": "goal", "link_id": g.ID})
	if len(got.Items) != 1 || got.Items[0].Title != "Stretch" {
		t.Fatalf("wanted only the goal's reminder: %+v", got.Items)
	}
	errorCode(t, s, "reminders_list", map[string]any{"link_entity": "goal"}, "invalid_input")
}
