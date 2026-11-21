package engine_test

import (
	"testing"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestTheLibraryListsArtifactsAndSubjectViews(t *testing.T) {
	s := integrationService(t)
	goal := execute[engine.Intention](t, s, "intentions_create", map[string]any{"idempotency_key": key(), "title": "Run a 5K"})
	viewInput := viewOf("intention", goal.ID, todoBlock)
	viewInput["tags"] = []string{" Training ", "training"}
	view := execute[engine.View](t, s, "views_create", viewInput)
	artifact := execute[engine.Artifact](t, s, "artifacts_create", map[string]any{
		"idempotency_key": key(), "title": "Race plan", "mode": "document", "catalog_id": engine.TrackerCatalogKit,
		"tags":  []string{"plan"},
		"pages": []map[string]any{{"id": "main", "sections": []map[string]any{{"id": "s", "title": "Plan", "blocks": []map[string]any{{"id": "t", "kind": "text", "body": "Easy week."}}}}}},
	})

	all := execute[engine.Page[engine.LibraryItem]](t, s, "library_list", map[string]any{})
	if all.Total != 2 || len(all.Items) != 2 {
		t.Fatalf("library: %+v", all)
	}
	byID := map[string]engine.LibraryItem{}
	for _, item := range all.Items {
		byID[item.ID] = item
	}
	if got := byID[view.ID]; got.Kind != "view" || got.Title != "Run a 5K" || got.SubjectID != goal.ID || got.Mode != "view" || len(got.Tags) != 1 || got.Tags[0] != "training" {
		t.Fatalf("view item: %+v", got)
	}
	if got := byID[artifact.ID]; got.Kind != "artifact" || got.Mode != "document" || got.Title != "Race plan" || got.SubjectID != "" {
		t.Fatalf("artifact item: %+v", got)
	}
	updated := execute[engine.View](t, s, "views_update", map[string]any{
		"idempotency_key": key(), "id": view.ID, "expected_version": view.Version, "metrics": view.Metrics, "pages": view.Pages, "tags": []string{"race day"},
	})
	if len(updated.Tags) != 1 || updated.Tags[0] != "race day" {
		t.Fatalf("updated tags: %+v", updated.Tags)
	}
	onlyViews := execute[engine.Page[engine.LibraryItem]](t, s, "library_list", map[string]any{"kind": "view"})
	if onlyViews.Total != 1 || onlyViews.Items[0].ID != view.ID {
		t.Fatalf("view filter: %+v", onlyViews)
	}

	viewDetail := execute[engine.LibraryDetail](t, s, "library_get", map[string]any{"id": view.ID})
	if viewDetail.Kind != "view" || viewDetail.SubjectKind != "intention" || len(viewDetail.Pages) != len(view.Pages) || viewDetail.Title != "Run a 5K" {
		t.Fatalf("view detail: %+v", viewDetail)
	}
	artifactDetail := execute[engine.LibraryDetail](t, s, "library_get", map[string]any{"id": artifact.ID})
	if artifactDetail.Kind != "artifact" || artifactDetail.Mode != "document" || len(artifactDetail.Pages) != 1 {
		t.Fatalf("artifact detail: %+v", artifactDetail)
	}
	execute[engine.Artifact](t, s, "artifacts_delete", map[string]any{
		"idempotency_key": key(), "id": artifact.ID, "expected_version": artifact.Version,
	})
	afterDelete := execute[engine.Page[engine.LibraryItem]](t, s, "library_list", map[string]any{})
	if afterDelete.Total != 1 || afterDelete.Items[0].ID != view.ID {
		t.Fatalf("library after delete: %+v", afterDelete)
	}
	errorCode(t, s, "library_get", map[string]any{"id": artifact.ID}, "not_found")
	errorCode(t, s, "library_get", map[string]any{"id": "not-a-uuid"}, "invalid_input")
}
