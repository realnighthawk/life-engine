package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// An artifact is {template, params, sources} (ADR 0011). A template is a whole screen each client draws natively, with
// typed slots; this registry is the one place that says which exist, what params they take and what their sources
// must return. templates_get serves it to the agent; the engine validates every write against it.

type Template struct {
	ID             string           `json:"id" jsonschema:"versioned template id to put in an artifact's template field, such as document.v1"`
	Title          string           `json:"title"`
	Description    string           `json:"description" jsonschema:"what the template is for and when to choose it"`
	ParamsSchema   json.RawMessage  `json:"params_schema" jsonschema:"JSON Schema of the artifact's params for this template"`
	DataContract   json.RawMessage  `json:"data_contract,omitempty" jsonschema:"JSON Schema of what each source must return; absent when the template reads no sources"`
	Example        json.RawMessage  `json:"example" jsonschema:"a valid params object to copy the shape from"`
	ExampleSources []ArtifactSource `json:"example_sources,omitempty" jsonschema:"the sources the example's params refer to"`
	DataExample    json.RawMessage  `json:"data_example,omitempty" jsonschema:"a valid answer from a source: build the app to return this shape"`
	Clients        []string         `json:"clients" jsonschema:"the apps that draw this template; a client that does not draw it shows the title only"`

	schema   *jsonschema.Resolved
	contract *jsonschema.Resolved
	check    func(params json.RawMessage, sources []ArtifactSource) error
}

type TemplatesGetInput struct {
	ID string `json:"id,omitempty" jsonschema:"one template id; omit to list them all"`
}
type TemplateList struct {
	Items []Template `json:"items"`
}

// Document and deck share one params shape: pages of sections of blocks. In a deck each page is a slide.
type DocBlock struct {
	ID       string   `json:"id" jsonschema:"stable lowercase identifier, unique within the artifact"`
	Kind     string   `json:"kind" jsonschema:"markdown, text or link"`
	Title    string   `json:"title,omitempty" jsonschema:"optional short heading, up to 80 characters"`
	Markdown string   `json:"markdown,omitempty" jsonschema:"markdown: authored Markdown, 1-16384 bytes; rendered without raw HTML"`
	Body     string   `json:"body,omitempty" jsonschema:"text: plain words, up to 280 characters"`
	Points   []string `json:"points,omitempty" jsonschema:"text: up to five short points, each up to 120 characters"`
	URL      string   `json:"url,omitempty" jsonschema:"link: an http or https address to open"`
}
type DocSection struct {
	ID     string     `json:"id" jsonschema:"stable lowercase identifier, unique within the artifact"`
	Title  string     `json:"title" jsonschema:"1-60 characters"`
	Intro  string     `json:"intro,omitempty" jsonschema:"optional one or two plain sentences, up to 240 characters"`
	Blocks []DocBlock `json:"blocks" jsonschema:"1-12 blocks in display order"`
}
type DocPage struct {
	ID       string       `json:"id" jsonschema:"stable lowercase identifier, unique within the artifact"`
	Title    string       `json:"title,omitempty" jsonschema:"tab or slide name, up to 80 characters; required when there is more than one page"`
	Sections []DocSection `json:"sections" jsonschema:"1-12 sections in display order"`
}
type DocParams struct {
	Pages []DocPage `json:"pages" jsonschema:"1-60 pages; one page shows no tabs"`
}

var artifactIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func checkDocument(raw json.RawMessage, _ []ArtifactSource) error {
	var p DocParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return invalid("params: " + err.Error())
	}
	if len(p.Pages) < 1 || len(p.Pages) > 60 {
		return invalid("A document or deck has 1-60 pages")
	}
	seen, blocks := map[string]bool{}, 0
	unique := func(kind, id string) bool {
		if !artifactIdentifier.MatchString(id) || seen[kind+id] {
			return false
		}
		seen[kind+id] = true
		return true
	}
	for _, pg := range p.Pages {
		if !unique("page", pg.ID) || len(pg.Title) > 80 || len(p.Pages) > 1 && strings.TrimSpace(pg.Title) == "" {
			return invalid("Page ids must be unique identifiers; a page title is at most 80 characters and required when there are several pages")
		}
		if len(pg.Sections) < 1 || len(pg.Sections) > 12 {
			return invalid("A page has 1-12 sections")
		}
		for _, sec := range pg.Sections {
			if !unique("section", sec.ID) || strings.TrimSpace(sec.Title) == "" || len(sec.Title) > 60 || len(sec.Intro) > 240 {
				return invalid("Section ids must be unique identifiers, a section title is 1-60 characters and its intro at most 240")
			}
			if len(sec.Blocks) < 1 || len(sec.Blocks) > 12 {
				return invalid("A section has 1-12 blocks")
			}
			for _, b := range sec.Blocks {
				if blocks++; blocks > 300 {
					return invalid("An artifact has at most 300 blocks in all")
				}
				if !unique("block", b.ID) || len(b.Title) > 80 {
					return invalid("Block ids must be unique identifiers and a heading is at most 80 characters")
				}
				if err := checkDocBlock(b); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkDocBlock(b DocBlock) error {
	only := func(fields ...bool) bool { // the block may set only the fields its kind takes
		for _, set := range fields {
			if set {
				return false
			}
		}
		return true
	}
	switch b.Kind {
	case "markdown":
		if strings.TrimSpace(b.Markdown) == "" || len(b.Markdown) > 16<<10 {
			return invalid("Markdown must contain text and be at most 16384 bytes")
		}
		if !only(b.Body != "", len(b.Points) > 0, b.URL != "") {
			return invalid("A markdown block takes only markdown")
		}
	case "text":
		if strings.TrimSpace(b.Body) == "" || len(b.Body) > 280 {
			return invalid("Text is 1-280 characters")
		}
		if len(b.Points) > 5 {
			return invalid("A text block has at most five points")
		}
		for _, pt := range b.Points {
			if strings.TrimSpace(pt) == "" || len(pt) > 120 {
				return invalid("A point is 1-120 characters")
			}
		}
		if !only(b.Markdown != "", b.URL != "") {
			return invalid("A text block takes only body and points")
		}
	case "link":
		if !only(b.Markdown != "", b.Body != "", len(b.Points) > 0) {
			return invalid("A link block takes only url")
		}
		return validateLink(b.URL)
	default:
		return invalid("Unknown block kind " + b.Kind + "; a document takes markdown, text and link blocks")
	}
	return nil
}

func validateLink(raw string) error {
	if u, e := url.Parse(raw); e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 2048 {
		return invalid("A link block needs an http or https url")
	}
	return nil
}

func docExample(deck bool) json.RawMessage {
	title := "Weekend plan"
	if deck {
		title = "Trip review"
	}
	return json.RawMessage(fmt.Sprintf(`{"pages":[{"id":"main","sections":[{"id":"plan","title":%q,"intro":"Where things stand.","blocks":[{"id":"notes","kind":"markdown","markdown":"## Saturday\n- Market at 9\n- Dinner at 7"},{"id":"more","kind":"link","title":"Booking","url":"https://example.com/booking"}]}]}]}`, title))
}

// templateList is the registry in display order; templateIndex looks one up by id.
var templateList, templateIndex = buildTemplates()

func buildTemplates() ([]*Template, map[string]*Template) {
	params, err := jsonschema.For[DocParams](nil)
	if err != nil {
		panic(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	resolved, err := params.Resolve(nil)
	if err != nil {
		panic(err)
	}
	clients := []string{"web", "ios"}
	list := []*Template{
		{
			ID: "document.v1", Title: "Document",
			Description:  "Authored content: a plan, report, booking, analysis or reference, as pages of titled sections of Markdown, short text and links. The default when nothing richer fits.",
			ParamsSchema: raw, Example: docExample(false), Clients: clients, schema: resolved, check: checkDocument,
		},
		{
			ID: "deck.v1", Title: "Deck",
			Description:  "The document shape shown one page at a time: each page is a slide.",
			ParamsSchema: raw, Example: docExample(true), Clients: clients, schema: resolved, check: checkDocument,
		},
	}
	list = append(list, boardTemplate())
	list = append(list, dataTemplates()...)
	index := make(map[string]*Template, len(list))
	for _, t := range list {
		index[t.ID] = t
	}
	return list, index
}

func templateIDs() string {
	ids := make([]string, len(templateList))
	for i, t := range templateList {
		ids[i] = t.ID
	}
	return strings.Join(ids, ", ")
}

func getTemplates(_ context.Context, _ *unit, in TemplatesGetInput) (TemplateList, error) {
	out := TemplateList{Items: []Template{}}
	for _, t := range templateList {
		if in.ID == "" || in.ID == t.ID {
			out.Items = append(out.Items, *t)
		}
	}
	if in.ID != "" && len(out.Items) == 0 {
		return out, invalid("Unknown template " + in.ID + "; use one of " + templateIDs())
	}
	return out, nil
}

// An artifact's data comes from sources it names; the engine only checks that each is well formed. The client fetches
// them, so a source that is down is the template's own error state, never a failed write.
type ArtifactSource struct {
	ID  string `json:"id" jsonschema:"lowercase identifier the template's params use to refer to this source"`
	URL string `json:"url" jsonschema:"life-app://<app>/<path>: an app of this person, reached through the router at /apps/<app>/<path>"`
}

var appName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

func validateSources(sources []ArtifactSource) error {
	if len(sources) > 8 {
		return invalid("An artifact has at most 8 sources")
	}
	seen := map[string]bool{}
	for _, s := range sources {
		if !artifactIdentifier.MatchString(s.ID) || seen[s.ID] {
			return invalid("Source ids must be unique identifiers")
		}
		seen[s.ID] = true
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme != "life-app" || !appName.MatchString(u.Host) || u.User != nil || u.Fragment != "" ||
			len(s.URL) > 2048 || strings.Contains(u.Path, "..") {
			return invalid("A source url is life-app://<app>/<path>, where app is lowercase letters, digits and hyphens")
		}
	}
	return nil
}

// validateArtifact checks a write against its template: the params schema first, so the error names the offending
// path, then the template's own rules.
func validateArtifact(d ArtifactData) error {
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 200 {
		return invalid("Artifact title must be 1-200 bytes")
	}
	t, ok := templateIndex[d.Template]
	if !ok {
		return invalid("Unknown template " + d.Template + "; use one of " + templateIDs() + " (templates_get describes them)")
	}
	var instance any
	if len(d.Params) == 0 || json.Unmarshal(d.Params, &instance) != nil {
		return invalid("params must be a JSON object")
	}
	if err := t.schema.Validate(instance); err != nil {
		return invalid("params do not match " + t.ID + ": " + err.Error())
	}
	if err := t.check(d.Params, d.Sources); err != nil {
		return err
	}
	return validateSources(d.Sources)
}
