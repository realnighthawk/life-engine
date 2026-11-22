package engine

import (
	"encoding/json"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// The data-driven templates read one source, an app's endpoint, and may call that same app back. The backend does every
// calculation; the template only draws what the source returns, so there is nothing to compute in the artifact.

// Collection: a list or table of records with optional per-row actions (a checklist is a collection with a done action).
type Column struct {
	Key    string `json:"key" jsonschema:"the field of each item this column shows; lowercase identifier"`
	Label  string `json:"label" jsonschema:"column heading, up to 24 characters"`
	Format string `json:"format,omitempty" jsonschema:"text (default), number, money, percent, date or status"`
}
type RowAction struct {
	ID          string `json:"id" jsonschema:"lowercase identifier, unique among the actions"`
	Label       string `json:"label" jsonschema:"button text, up to 24 characters"`
	Method      string `json:"method" jsonschema:"POST or DELETE"`
	Path        string `json:"path" jsonschema:"a path on the source's app with {id} replaced by the row's id, such as /items/{id}/done"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"ask the person before sending"`
	Destructive bool   `json:"destructive,omitempty" jsonschema:"draw it as a destructive action"`
}
type CollectionParams struct {
	Source  string      `json:"source" jsonschema:"the id of one of the artifact's sources; it returns the items"`
	Layout  string      `json:"layout" jsonschema:"list shows the first column as the title, the second as the subtitle and the rest beside it; table shows every column"`
	Columns []Column    `json:"columns" jsonschema:"1-8 columns, in order"`
	Empty   string      `json:"empty,omitempty" jsonschema:"what to say when there are no items, up to 120 characters"`
	Actions []RowAction `json:"actions,omitempty" jsonschema:"up to 4 actions offered on every row"`
}

// Tracker: one series over time, optionally with a target, and a form that logs a new value.
type LogField struct {
	Key      string `json:"key" jsonschema:"the field name sent to the app; lowercase identifier"`
	Label    string `json:"label" jsonschema:"up to 24 characters"`
	Kind     string `json:"kind" jsonschema:"number, text or date"`
	Required bool   `json:"required,omitempty"`
}
type TrackerLog struct {
	Label  string     `json:"label" jsonschema:"the form's button text, up to 24 characters"`
	Path   string     `json:"path" jsonschema:"a path on the source's app that receives the form as a JSON POST, such as /log"`
	Fields []LogField `json:"fields" jsonschema:"1-4 fields"`
}
type TrackerParams struct {
	Source    string      `json:"source" jsonschema:"the id of one of the artifact's sources; it returns the series"`
	Unit      string      `json:"unit,omitempty" jsonschema:"what a value is measured in, up to 24 characters"`
	Target    *float64    `json:"target,omitempty" jsonschema:"a value to draw a line at"`
	Direction string      `json:"direction,omitempty" jsonschema:"up or down: which way is better, so progress is drawn correctly"`
	Days      int         `json:"days,omitempty" jsonschema:"how many recent days to show, 1-366; default 30"`
	Log       *TrackerLog `json:"log,omitempty" jsonschema:"a form that records a new value; omit for a read-only tracker"`
}

var (
	columnFormats = map[string]bool{"": true, "text": true, "number": true, "money": true, "percent": true, "date": true, "status": true}
	logKinds      = map[string]bool{"number": true, "text": true, "date": true}
)

func declared(sources []ArtifactSource, id string) bool {
	for _, s := range sources {
		if s.ID == id {
			return true
		}
	}
	return false
}

// appPath is a path on an app: it starts with one slash, is not a URL, and cannot climb out of the app.
func appPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && len(p) <= 200 &&
		!strings.ContainsAny(p, "?# \\") && !strings.Contains(p, "..") && !strings.Contains(p, "://")
}

func checkCollection(raw json.RawMessage, sources []ArtifactSource) error {
	var p CollectionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return invalid("params: " + err.Error())
	}
	if !declared(sources, p.Source) {
		return invalid("params.source must be the id of one of the artifact's sources")
	}
	if !oneOf(p.Layout, "list", "table") {
		return invalid("layout is list or table")
	}
	if len(p.Columns) < 1 || len(p.Columns) > 8 || len(p.Empty) > 120 || len(p.Actions) > 4 {
		return invalid("A collection has 1-8 columns, an empty message up to 120 characters and at most 4 actions")
	}
	keys := map[string]bool{}
	for _, c := range p.Columns {
		if !artifactIdentifier.MatchString(c.Key) || keys[c.Key] || strings.TrimSpace(c.Label) == "" || len(c.Label) > 24 || !columnFormats[c.Format] {
			return invalid("Column keys are unique identifiers, a label is 1-24 characters and a format is text, number, money, percent, date or status")
		}
		keys[c.Key] = true
	}
	ids := map[string]bool{}
	for _, a := range p.Actions {
		if !artifactIdentifier.MatchString(a.ID) || ids[a.ID] || strings.TrimSpace(a.Label) == "" || len(a.Label) > 24 ||
			!oneOf(a.Method, "POST", "DELETE") || !appPath(a.Path) {
			return invalid("Action ids are unique identifiers, a label is 1-24 characters, the method is POST or DELETE and the path is a path on the app, such as /items/{id}/done")
		}
		ids[a.ID] = true
	}
	return nil
}

func checkTracker(raw json.RawMessage, sources []ArtifactSource) error {
	var p TrackerParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return invalid("params: " + err.Error())
	}
	if !declared(sources, p.Source) {
		return invalid("params.source must be the id of one of the artifact's sources")
	}
	if len(p.Unit) > 24 || p.Days < 0 || p.Days > 366 || p.Direction != "" && !oneOf(p.Direction, "up", "down") {
		return invalid("unit is at most 24 characters, days 1-366, direction up or down")
	}
	if l := p.Log; l != nil {
		if strings.TrimSpace(l.Label) == "" || len(l.Label) > 24 || !appPath(l.Path) || len(l.Fields) < 1 || len(l.Fields) > 4 {
			return invalid("A log form has a label of 1-24 characters, a path on the app and 1-4 fields")
		}
		keys := map[string]bool{}
		for _, f := range l.Fields {
			if !artifactIdentifier.MatchString(f.Key) || keys[f.Key] || strings.TrimSpace(f.Label) == "" || len(f.Label) > 24 || !logKinds[f.Kind] {
				return invalid("Log field keys are unique identifiers, a label is 1-24 characters and a kind is number, text or date")
			}
			keys[f.Key] = true
		}
	}
	return nil
}

// What each template's source must return. These are deliberately liberal about extra fields so a backend can add its own.
const collectionContract = `{"type":"object","required":["items"],"properties":{"items":{"type":"array","maxItems":500,"items":{"type":"object","required":["id"],"properties":{"id":{"type":"string","minLength":1}},"additionalProperties":{"type":["string","number","boolean","null"]}}}}}`
const trackerContract = `{"type":"object","required":["points"],"properties":{"points":{"type":"array","maxItems":2000,"items":{"type":"object","required":["at","value"],"properties":{"at":{"type":"string","minLength":8,"description":"a date (2026-11-21) or an RFC 3339 time"},"value":{"type":"number"}}}}}}`

const collectionExample = `{"source":"chores","layout":"list","columns":[{"key":"title","label":"Chore"},{"key":"due","label":"Due","format":"date"},{"key":"status","label":"Status","format":"status"}],"empty":"Nothing to do.","actions":[{"id":"done","label":"Done","method":"POST","path":"/chores/{id}/done"}]}`
const collectionData = `{"items":[{"id":"c1","title":"Water the plants","due":"2026-11-22","status":"open"},{"id":"c2","title":"Take out the bins","due":"2026-11-23","status":"open"}]}`
const trackerExample = `{"source":"weight","unit":"kg","target":72,"direction":"down","days":30,"log":{"label":"Log weight","path":"/log","fields":[{"key":"value","label":"Weight","kind":"number","required":true},{"key":"note","label":"Note","kind":"text"}]}}`
const trackerData = `{"points":[{"at":"2026-11-01","value":75.2},{"at":"2026-11-08","value":74.6},{"at":"2026-11-15","value":74.1}]}`

func mustSchema[T any]() (json.RawMessage, *jsonschema.Resolved) {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	return mustMarshalSchema(s)
}

func mustMarshalSchema(s *jsonschema.Schema) (json.RawMessage, *jsonschema.Resolved) {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	r, err := s.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return raw, r
}

func contract(src string) (json.RawMessage, *jsonschema.Resolved) {
	var s jsonschema.Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		panic(err)
	}
	return mustMarshalSchema(&s)
}

func dataTemplates() []*Template {
	collParams, collSchema := mustSchema[CollectionParams]()
	collRaw, collResolved := contract(collectionContract)
	trackParams, trackSchema := mustSchema[TrackerParams]()
	trackRaw, trackResolved := contract(trackerContract)
	sourceFor := func(id, app, path string) []ArtifactSource {
		return []ArtifactSource{{ID: id, URL: "life-app://" + app + path}}
	}
	return []*Template{
		{
			ID: "collection.v1", Title: "Collection",
			Description:  "A list or table of records the person can act on: chores, a shopping list, bookings, open items. The source returns the records; each action calls the same app (for example marking one done).",
			ParamsSchema: collParams, DataContract: collRaw, Example: json.RawMessage(collectionExample), ExampleSources: sourceFor("chores", "chores", "/chores"),
			DataExample: json.RawMessage(collectionData), Clients: []string{}, schema: collSchema, contract: collResolved, check: checkCollection,
		},
		{
			ID: "tracker.v1", Title: "Tracker",
			Description:  "One measurement over time, drawn as a chart with an optional target, plus an optional form that logs a new value: weight, spend, a habit count. The source returns the points; the form posts to the same app.",
			ParamsSchema: trackParams, DataContract: trackRaw, Example: json.RawMessage(trackerExample), ExampleSources: sourceFor("weight", "weight", "/series"),
			DataExample: json.RawMessage(trackerData), Clients: []string{}, schema: trackSchema, contract: trackResolved, check: checkTracker,
		},
	}
}

// CheckData reports whether a source's answer meets the template's data contract.
func (t *Template) CheckData(raw []byte) error {
	if t.contract == nil {
		return invalid(t.ID + " reads no source")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return invalid("source data is not JSON")
	}
	if err := t.contract.Validate(v); err != nil {
		return invalid("source data does not meet the " + t.ID + " data contract: " + err.Error())
	}
	return nil
}
