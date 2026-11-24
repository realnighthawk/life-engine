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

func TestBoardRefusesWhatItCannotDraw(t *testing.T) {
	sec := func(body string) string { return `{"headline":"h","sections":[` + body + `]}` }
	// A header is optional: a board can be only components.
	if err := validateArtifact(ArtifactData{Title: "t", Template: "board.v1", Params: json.RawMessage(`{"sections":[{"id":"a","kind":"moves","moves":[{"id":"m","title":"A"}]}]}`)}); err != nil {
		t.Errorf("a board without a header was refused: %v", err)
	}
	moves := `{"id":"a","kind":"moves","moves":[{"id":"m","title":"A"}]}`
	cases := map[string]struct{ params, want string }{
		"no sections":       {`{"headline":"h","sections":[]}`, "1-12 sections"},
		"long eyebrow":      {`{"eyebrow":"` + strings.Repeat("x", 41) + `","sections":[` + moves + `]}`, "eyebrow"},
		"long headline":     {`{"headline":"` + strings.Repeat("x", 91) + `","sections":[` + moves + `]}`, "headline"},
		"bad tone":          {`{"headline":"h","tone":"red","sections":[` + moves + `]}`, "tone"},
		"unknown kind":      {sec(`{"id":"a","kind":"gauge"}`), "Unknown section kind"},
		"foreign field":     {sec(`{"id":"a","kind":"moves","moves":[{"id":"m","title":"A"}],"links":[{"id":"l","title":"L","url":"https://x.example"}]}`), "takes only moves"},
		"empty component":   {sec(`{"id":"a","kind":"moves"}`), "1-8 moves"},
		"bad icon":          {sec(`{"id":"a","kind":"moves","moves":[{"id":"m","title":"A","icon":"rocket"}]}`), "board icon"},
		"bad state":         {sec(`{"id":"a","kind":"moves","moves":[{"id":"m","title":"A","state":"later"}]}`), "state"},
		"hyphen id":         {sec(`{"id":"a-b","kind":"moves","moves":[{"id":"m","title":"A"}]}`), "unique id"},
		"duplicate id":      {sec(moves + "," + moves), "unique id"},
		"five stats":        {sec(`{"id":"a","kind":"stats","stats":[` + strings.TrimSuffix(strings.Repeat(`{"id":"s","label":"L","value":"1"},`, 5), ",") + `]}`), "1-4 stats"},
		"long stat value":   {sec(`{"id":"a","kind":"stats","stats":[{"id":"s","label":"L","value":"1234567890123"}]}`), "stat"},
		"callout no text":   {sec(`{"id":"a","kind":"callout","callout":{"text":" "}}`), "text up to 160"},
		"table ragged row":  {sec(`{"id":"a","kind":"table","table":{"columns":[{"label":"A"},{"label":"B"}],"rows":[{"id":"r","cells":[{"text":"x"}]}]}}`), "one cell per column"},
		"table one column":  {sec(`{"id":"a","kind":"table","table":{"columns":[{"label":"A"}],"rows":[{"id":"r","cells":[{"text":"x"}]}]}}`), "2-5 columns"},
		"one bar":           {sec(`{"id":"a","kind":"bars","bars":{"bars":[{"id":"b","label":"B","value":1}]}}`), "2-8 bars"},
		"negative bar":      {sec(`{"id":"a","kind":"bars","bars":{"bars":[{"id":"b","label":"B","value":-1},{"id":"c","label":"C","value":1}]}}`), "value of zero or more"},
		"chart style":       {sec(`{"id":"a","kind":"chart","chart":{"style":"pie","points":[{"label":"a","value":1},{"label":"b","value":2}]}}`), "line or column"},
		"chart one point":   {sec(`{"id":"a","kind":"chart","chart":{"style":"line","points":[{"label":"a","value":1}]}}`), "2-24 points"},
		"progress zero max": {sec(`{"id":"a","kind":"progress","progress":[{"id":"p","label":"P","value":1,"max":0}]}`), "max above zero"},
		"bad link":          {sec(`{"id":"a","kind":"links","links":[{"id":"l","title":"L","url":"javascript:x"}]}`), "http"},
		"unknown field":     {`{"headline":"h","sections":[` + moves + `],"colour":"red"}`, "do not match"},
	}
	for name, c := range cases {
		err := validateArtifact(ArtifactData{Title: "t", Template: "board.v1", Params: json.RawMessage(c.params)})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
	// Every component the brief names must be drawable from a minimal valid instance.
	valid := []string{
		moves,
		`{"id":"a","kind":"callout","callout":{"text":"Heads up","tone":"warn","icon":"alert"}}`,
		`{"id":"a","kind":"facts","facts":[{"id":"f","label":"Budget","value":"£100.9m","icon":"money"}]}`,
		`{"id":"a","kind":"table","table":{"columns":[{"label":"Player"},{"label":"Pts","align":"end"}],"rows":[{"id":"r","cells":[{"text":"Trafford"},{"text":"24.9","tone":"good"}]}]}}`,
		`{"id":"a","kind":"bars","bars":{"max":30,"bars":[{"id":"b","label":"B","value":24.9,"display":"24.9"},{"id":"c","label":"C","value":12}]}}`,
		`{"id":"a","kind":"chart","chart":{"style":"column","unit":"pts","target":60,"points":[{"label":"GW9","value":55},{"label":"GW10","value":61}]}}`,
		`{"id":"a","kind":"progress","progress":[{"id":"p","label":"Chips used","value":1,"max":4,"note":"1 of 4"}]}`,
	}
	for _, v := range valid {
		if err := validateArtifact(ArtifactData{Title: "t", Template: "board.v1", Params: json.RawMessage(sec(v))}); err != nil {
			t.Errorf("a valid section was refused: %v\n%s", err, v)
		}
	}
}
