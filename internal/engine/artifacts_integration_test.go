package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/realnighthawk/life-engine/internal/engine"
	"github.com/realnighthawk/life-engine/internal/transport"
)

func TestStandaloneArtifactReplayAndRevision(t *testing.T) {
	s := integrationService(t)
	handler, err := transport.New(s, http.NewServeMux(), nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Agent-ID", "direct-plugin")
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	deck := func(markdown string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"pages": []map[string]any{{"id": "main", "sections": []map[string]any{{"id": "review", "title": "Tradeoffs",
			"blocks": []map[string]any{{"id": "draft", "kind": "markdown", "markdown": markdown}}}}}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	data := engine.ArtifactData{Title: "Design review", Template: "deck.v1", Params: deck("## Original")}
	const reviewer = "agent:reviewer:discord:channel:c1"
	call := func(sessionKey, operation string, input any) engine.Artifact {
		t.Helper()
		params := &mcp.CallToolParams{Name: operation, Arguments: input}
		if sessionKey != "" {
			params.Meta = map[string]any{engine.SessionMetaKey: sessionKey}
		}
		result, err := session.CallTool(ctx, params)
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %+v", operation, err, result)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var a engine.Artifact
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	create := engine.CreateArtifactInput{
		Meta: engine.Meta{IdempotencyKey: key()}, ArtifactData: data,
		Tags: []string{"  Design ", "DESIGN", " ", "Review"},
	}
	a := call(branch, "artifacts_create", create)
	if a.AgentID != "main" || a.Version != 1 || a.UpdatedAt.IsZero() || !slices.Equal(a.Tags, []string{"design", "review"}) {
		t.Fatalf("create metadata: %+v", a)
	}
	if again := call(reviewer, "artifacts_create", create); again.ID != a.ID || again.AgentID != a.AgentID ||
		again.Version != 1 || !again.UpdatedAt.Equal(a.UpdatedAt) || !slices.Equal(again.Tags, a.Tags) {
		t.Fatalf("create replay: %+v", again)
	}
	create.IdempotencyKey = key()
	other := call(reviewer, "artifacts_create", create)
	if other.AgentID != "reviewer" || other.ID == a.ID {
		t.Fatalf("second creator: %+v", other)
	}
	// No session (as with HTTP callers and legacy artifacts) must not invent a creator.
	create.IdempotencyKey, create.Tags = key(), nil
	unattributed := execute[engine.Artifact](t, s, "artifacts_create", create)
	if unattributed.AgentID != "" || unattributed.Tags == nil || len(unattributed.Tags) != 0 {
		t.Fatalf("unattributed artifact: %+v", unattributed)
	}
	create.IdempotencyKey = key()
	direct := call("", "artifacts_create", create)
	if direct.AgentID != "direct-plugin" {
		t.Fatalf("direct MCP attribution: %+v", direct)
	}
	forged := map[string]any{
		"idempotency_key": key(), "title": data.Title, "template": data.Template,
		"params": data.Params, "agent_id": "main",
	}
	errorCode(t, s, "artifacts_create", forged, "invalid_input")
	data.Params = deck("## Revised")
	edit := engine.UpdateArtifactInput{EditInput: engine.EditInput{ID: a.ID, ExpectedVersion: a.Version, Meta: engine.Meta{IdempotencyKey: key()}}, ArtifactData: data}
	updated := call(reviewer, "artifacts_update", edit)
	if updated.AgentID != a.AgentID || updated.Version != 2 || updated.UpdatedAt.Before(a.UpdatedAt) || !slices.Equal(updated.Tags, a.Tags) {
		t.Fatalf("update metadata: %+v", updated)
	}
	if again := call(reviewer, "artifacts_update", edit); again.Version != 2 || !again.UpdatedAt.Equal(updated.UpdatedAt) ||
		again.AgentID != a.AgentID || !slices.Equal(again.Tags, a.Tags) {
		t.Fatalf("update replay: %+v", again)
	}
	edit.IdempotencyKey = key()
	errorCode(t, s, "artifacts_update", edit, "conflict")
	got := execute[engine.Artifact](t, s, "artifacts_get", engine.GetInput{ID: a.ID})
	if got.Version != 2 || got.Template != "deck.v1" || got.AgentID != a.AgentID || !got.UpdatedAt.Equal(updated.UpdatedAt) ||
		!slices.Equal(got.Tags, a.Tags) || !bytes.Contains(got.Params, []byte("## Revised")) {
		t.Fatalf("stored artifact: %+v", got)
	}

	first := execute[engine.Page[engine.ArtifactSummary]](t, s, "artifacts_list", engine.ListArtifactsInput{Limit: 1})
	if first.Total != 4 || len(first.Items) != 1 || first.Items[0].ID != a.ID || first.NextOffset == nil || *first.NextOffset != 1 {
		t.Fatalf("first page: %+v", first)
	}
	rest := execute[engine.Page[engine.ArtifactSummary]](t, s, "artifacts_list", engine.ListArtifactsInput{Offset: *first.NextOffset})
	if rest.Total != 4 || len(rest.Items) != 3 || rest.NextOffset != nil || rest.Items[0].ID != direct.ID ||
		rest.Items[1].ID != unattributed.ID || rest.Items[1].AgentID != "" || rest.Items[2].ID != other.ID {
		t.Fatalf("remaining page: %+v", rest)
	}
	for _, want := range []engine.Artifact{got, other, direct} {
		page := execute[engine.Page[engine.ArtifactSummary]](t, s, "artifacts_list", engine.ListArtifactsInput{AgentID: want.AgentID})
		if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != want.ID || page.Items[0].Title != want.Title ||
			page.Items[0].Template != want.Template ||
			page.Items[0].AgentID != want.AgentID || page.Items[0].Version != want.Version ||
			!page.Items[0].UpdatedAt.Equal(want.UpdatedAt) || !slices.Equal(page.Items[0].Tags, want.Tags) {
			t.Fatalf("creator page for %s: %+v", want.AgentID, page)
		}
	}
	missing := execute[engine.Page[engine.ArtifactSummary]](t, s, "artifacts_list", engine.ListArtifactsInput{AgentID: "absent"})
	if missing.Total != 0 || missing.Items == nil || len(missing.Items) != 0 || missing.NextOffset != nil {
		t.Fatalf("missing creator page: %+v", missing)
	}
	unattributedEdit := engine.UpdateArtifactInput{
		EditInput:    engine.EditInput{ID: unattributed.ID, ExpectedVersion: 1, Meta: engine.Meta{IdempotencyKey: key()}},
		ArtifactData: data,
	}
	if after := call(branch, "artifacts_update", unattributedEdit); after.AgentID != "" {
		t.Fatalf("update attributed a creatorless artifact: %+v", after)
	}
	emptyTags := []string{}
	edit.Tags = &emptyTags
	edit.ExpectedVersion = got.Version
	cleared := execute[engine.Artifact](t, s, "artifacts_update", edit)
	if cleared.Version != 3 || cleared.Tags == nil || len(cleared.Tags) != 0 || cleared.AgentID != a.AgentID {
		t.Fatalf("clear tags: %+v", cleared)
	}
	edit.Tags = nil
	errorCode(t, s, "artifacts_update", edit, "conflict")
}

func TestDataTemplatesRoundTripThroughTheEngine(t *testing.T) {
	s := integrationService(t)
	tpls := execute[engine.TemplateList](t, s, "templates_get", map[string]any{})
	if len(tpls.Items) != 4 {
		t.Fatalf("templates: %+v", tpls.Items)
	}
	for _, tpl := range tpls.Items {
		created := execute[engine.Artifact](t, s, "artifacts_create", engine.CreateArtifactInput{
			Meta:         engine.Meta{IdempotencyKey: key()},
			ArtifactData: engine.ArtifactData{Title: "Example " + tpl.ID, Template: tpl.ID, Params: tpl.Example, Sources: tpl.ExampleSources},
		})
		got := execute[engine.Artifact](t, s, "artifacts_get", engine.GetInput{ID: created.ID})
		if got.Template != tpl.ID || len(got.Sources) != len(tpl.ExampleSources) || !bytes.Contains(got.Params, []byte(`"`)) {
			t.Fatalf("%s did not round-trip: %+v", tpl.ID, got)
		}
	}
	one := execute[engine.TemplateList](t, s, "templates_get", map[string]any{"id": "tracker.v1"})
	if len(one.Items) != 1 || one.Items[0].DataContract == nil || one.Items[0].DataExample == nil {
		t.Fatalf("tracker template: %+v", one.Items)
	}
	errorCode(t, s, "templates_get", map[string]any{"id": "nope.v9"}, "invalid_input")
}
