package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every template's example must pass its own schema and rules, or templates_get would teach the agent a shape the
// engine refuses.
func TestTemplateExamplesAreValid(t *testing.T) {
	for _, tpl := range templateList {
		err := validateArtifact(ArtifactData{Title: "x", Template: tpl.ID, Params: tpl.Example})
		if err != nil {
			t.Errorf("%s example: %v", tpl.ID, err)
		}
	}
}

func TestArtifactValidationRefusesWhatTheTemplateDoesNot(t *testing.T) {
	good := func() map[string]any {
		return map[string]any{"pages": []any{map[string]any{"id": "p", "sections": []any{map[string]any{"id": "s", "title": "S",
			"blocks": []any{map[string]any{"id": "b", "kind": "markdown", "markdown": "hi"}}}}}}}
	}
	cases := map[string]struct {
		template string
		mutate   func(p map[string]any)
		sources  []ArtifactSource
		want     string
	}{
		"unknown template": {template: "nope.v1", mutate: func(map[string]any) {}, want: "Unknown template"},
		"unknown field":    {template: "document.v1", mutate: func(p map[string]any) { p["extra"] = 1 }, want: "do not match"},
		"missing pages":    {template: "document.v1", mutate: func(p map[string]any) { delete(p, "pages") }, want: "do not match"},
		"old tasks block":  {template: "document.v1", mutate: func(p map[string]any) { setKind(p, "tasks") }, want: "Unknown block kind"},
		"foreign field":    {template: "document.v1", mutate: func(p map[string]any) { block(p)["url"] = "https://example.com" }, want: "takes only markdown"},
		"bad link": {template: "document.v1", mutate: func(p map[string]any) {
			setKind(p, "link")
			delete(block(p), "markdown")
			block(p)["url"] = "javascript:alert(1)"
		}, want: "http or https"},
		"source not an app":   {template: "document.v1", mutate: func(map[string]any) {}, sources: []ArtifactSource{{ID: "a", URL: "https://example.com/x"}}, want: "life-app://"},
		"source path escape":  {template: "document.v1", mutate: func(map[string]any) {}, sources: []ArtifactSource{{ID: "a", URL: "life-app://tracker/../life/x"}}, want: "life-app://"},
		"duplicate source id": {template: "document.v1", mutate: func(map[string]any) {}, sources: []ArtifactSource{{ID: "a", URL: "life-app://x/y"}, {ID: "a", URL: "life-app://x/z"}}, want: "unique"},
	}
	for name, c := range cases {
		p := good()
		c.mutate(p)
		raw, _ := json.Marshal(p)
		err := validateArtifact(ArtifactData{Title: "t", Template: c.template, Params: raw, Sources: c.sources})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
	raw, _ := json.Marshal(good())
	ok := ArtifactData{Title: "t", Template: "deck.v1", Params: raw, Sources: []ArtifactSource{{ID: "log", URL: "life-app://weight/series?days=30"}}}
	if err := validateArtifact(ok); err != nil {
		t.Errorf("valid artifact refused: %v", err)
	}
}

func block(p map[string]any) map[string]any {
	return p["pages"].([]any)[0].(map[string]any)["sections"].([]any)[0].(map[string]any)["blocks"].([]any)[0].(map[string]any)
}
func setKind(p map[string]any, kind string) { block(p)["kind"] = kind }
