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
		err := validateArtifact(ArtifactData{Title: "x", Template: tpl.ID, Params: tpl.Example, Sources: tpl.ExampleSources})
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

// The example a backend author copies must meet the contract the engine publishes, and a template with no data contract
// must offer no data example.
func TestDataExamplesMeetTheirContracts(t *testing.T) {
	for _, tpl := range templateList {
		if tpl.contract == nil {
			if tpl.DataExample != nil {
				t.Errorf("%s has a data example but no contract", tpl.ID)
			}
			continue
		}
		if err := tpl.CheckData(tpl.DataExample); err != nil {
			t.Errorf("%s data example: %v", tpl.ID, err)
		}
	}
}

func TestDataContractsRefuseWhatTheTemplateCannotDraw(t *testing.T) {
	coll, track := templateIndex["collection.v1"], templateIndex["tracker.v1"]
	for name, c := range map[string]struct {
		tpl  *Template
		data string
	}{
		"collection without items": {coll, `{}`},
		"item without id":          {coll, `{"items":[{"title":"x"}]}`},
		"nested value in an item":  {coll, `{"items":[{"id":"a","tags":["x"]}]}`},
		"tracker without points":   {track, `{"series":[]}`},
		"point value is a string":  {track, `{"points":[{"at":"2026-11-01","value":"75"}]}`},
		"point without a time":     {track, `{"points":[{"value":75}]}`},
		"not json":                 {track, `points`},
	} {
		if err := c.tpl.CheckData([]byte(c.data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := templateIndex["document.v1"].CheckData([]byte(`{}`)); err == nil {
		t.Error("a document reads no source")
	}
}

func TestDataTemplatesRefuseWhatTheyCannotRender(t *testing.T) {
	src := []ArtifactSource{{ID: "app", URL: "life-app://chores/list"}}
	cases := map[string]struct {
		template, params string
		sources          []ArtifactSource
		want             string
	}{
		"undeclared source":    {"collection.v1", `{"source":"nope","layout":"list","columns":[{"key":"a","label":"A"}]}`, src, "one of the artifact's sources"},
		"no sources at all":    {"tracker.v1", `{"source":"app"}`, nil, "one of the artifact's sources"},
		"bad layout":           {"collection.v1", `{"source":"app","layout":"grid","columns":[{"key":"a","label":"A"}]}`, src, "list or table"},
		"no columns":           {"collection.v1", `{"source":"app","layout":"list","columns":[]}`, src, "1-8 columns"},
		"duplicate column":     {"collection.v1", `{"source":"app","layout":"list","columns":[{"key":"a","label":"A"},{"key":"a","label":"B"}]}`, src, "unique identifiers"},
		"bad format":           {"collection.v1", `{"source":"app","layout":"list","columns":[{"key":"a","label":"A","format":"html"}]}`, src, "format"},
		"action method":        {"collection.v1", `{"source":"app","layout":"list","columns":[{"key":"a","label":"A"}],"actions":[{"id":"x","label":"X","method":"GET","path":"/x/{id}"}]}`, src, "POST or DELETE"},
		"action leaves app":    {"collection.v1", `{"source":"app","layout":"list","columns":[{"key":"a","label":"A"}],"actions":[{"id":"x","label":"X","method":"POST","path":"https://evil.example/x"}]}`, src, "path on the app"},
		"action protocol path": {"collection.v1", `{"source":"app","layout":"list","columns":[{"key":"a","label":"A"}],"actions":[{"id":"x","label":"X","method":"POST","path":"//evil.example/x"}]}`, src, "path on the app"},
		"action climbs":        {"collection.v1", `{"source":"app","layout":"list","columns":[{"key":"a","label":"A"}],"actions":[{"id":"x","label":"X","method":"POST","path":"/../life/x"}]}`, src, "path on the app"},
		"tracker days":         {"tracker.v1", `{"source":"app","days":999}`, src, "days 1-366"},
		"tracker direction":    {"tracker.v1", `{"source":"app","direction":"sideways"}`, src, "direction"},
		"log field kind":       {"tracker.v1", `{"source":"app","log":{"label":"L","path":"/log","fields":[{"key":"v","label":"V","kind":"file"}]}}`, src, "kind"},
		"log without fields":   {"tracker.v1", `{"source":"app","log":{"label":"L","path":"/log","fields":[]}}`, src, "1-4 fields"},
		"unknown param":        {"tracker.v1", `{"source":"app","colour":"red"}`, src, "do not match"},
	}
	for name, c := range cases {
		err := validateArtifact(ArtifactData{Title: "t", Template: c.template, Params: json.RawMessage(c.params), Sources: c.sources})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
}
