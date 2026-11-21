package engine_test

import (
	"testing"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestStandaloneArtifactReplayAndRevision(t *testing.T) {
	s := integrationService(t)
	data := engine.ArtifactData{
		Title: "Design review", Mode: "deck", CatalogID: engine.TrackerCatalogKit,
		Pages: []engine.ViewPage{{ID: "main", Sections: []engine.ViewSection{{ID: "review", Title: "Tradeoffs",
			Blocks: []engine.ViewBlock{{ID: "draft", Kind: "markdown", Markdown: "## Original"}},
		}}}},
	}
	create := engine.CreateArtifactInput{Meta: engine.Meta{IdempotencyKey: key()}, ArtifactData: data}
	a := execute[engine.Artifact](t, s, "artifacts_create", create)
	if again := execute[engine.Artifact](t, s, "artifacts_create", create); again.ID != a.ID || again.Version != 1 {
		t.Fatalf("create replay: %+v", again)
	}
	data.Pages[0].Sections[0].Blocks[0].Markdown = "## Revised"
	edit := engine.UpdateArtifactInput{EditInput: engine.EditInput{ID: a.ID, ExpectedVersion: a.Version, Meta: engine.Meta{IdempotencyKey: key()}}, ArtifactData: data}
	execute[engine.Artifact](t, s, "artifacts_update", edit)
	if again := execute[engine.Artifact](t, s, "artifacts_update", edit); again.Version != 2 {
		t.Fatalf("update replay: %+v", again)
	}
	edit.IdempotencyKey = key()
	errorCode(t, s, "artifacts_update", edit, "conflict")
	got := execute[engine.Artifact](t, s, "artifacts_get", engine.GetInput{ID: a.ID})
	if got.Version != 2 || got.Mode != "deck" || got.Pages[0].Sections[0].Blocks[0].Markdown != "## Revised" {
		t.Fatalf("stored artifact: %+v", got)
	}
}
