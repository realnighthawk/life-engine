package engine

import (
	"encoding/json"
	"math"
	"strings"
)

// Board: a screen of typed components for any task, drawn natively by every client. An optional header gives a title or a
// verdict; then an ordered
// list of sections, each one component from a closed set (stats, moves, watch, steps, callout, facts, table, bars, chart,
// progress, details, links). The clients draw every component natively, mostly with icon, tone and shape, so each text
// slot is short on purpose: a long explanation belongs in a document, or in a folded details section.

type BoardStat struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Label string `json:"label" jsonschema:"what the number is, up to 20 characters"`
	Value string `json:"value" jsonschema:"the figure as shown, up to 12 characters, such as +12.9 or 5th"`
	Note  string `json:"note,omitempty" jsonschema:"a few words under the value, up to 24 characters"`
	Icon  string `json:"icon,omitempty" jsonschema:"one of the board icons"`
	Tone  string `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BoardMove struct {
	ID     string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Title  string `json:"title" jsonschema:"the action, up to 60 characters"`
	Detail string `json:"detail,omitempty" jsonschema:"one short reason, up to 80 characters"`
	State  string `json:"state,omitempty" jsonschema:"todo (default), done or hold"`
	Icon   string `json:"icon,omitempty" jsonschema:"one of the board icons"`
	Tag    string `json:"tag,omitempty" jsonschema:"a small label such as a deadline, up to 16 characters"`
}
type BoardWatch struct {
	ID     string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Title  string `json:"title" jsonschema:"the risk, up to 60 characters"`
	Detail string `json:"detail,omitempty" jsonschema:"one short note, up to 80 characters"`
	Level  string `json:"level,omitempty" jsonschema:"info, warn (default) or risk"`
	Icon   string `json:"icon,omitempty" jsonschema:"one of the board icons"`
}
type BoardStep struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Label string `json:"label" jsonschema:"when, up to 16 characters, such as GW15 or Jan"`
	Title string `json:"title" jsonschema:"what happens, up to 48 characters"`
	Icon  string `json:"icon,omitempty" jsonschema:"one of the board icons"`
	Tone  string `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BoardCallout struct {
	Title string `json:"title,omitempty" jsonschema:"up to 40 characters"`
	Text  string `json:"text" jsonschema:"one or two sentences, up to 160 characters"`
	Icon  string `json:"icon,omitempty" jsonschema:"one of the board icons"`
	Tone  string `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BoardFact struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Label string `json:"label" jsonschema:"up to 24 characters"`
	Value string `json:"value" jsonschema:"up to 40 characters"`
	Icon  string `json:"icon,omitempty" jsonschema:"one of the board icons"`
}
type BoardColumn struct {
	Label string `json:"label" jsonschema:"column heading, up to 16 characters"`
	Align string `json:"align,omitempty" jsonschema:"start (default) or end, for numbers"`
}
type BoardCell struct {
	Text string `json:"text" jsonschema:"up to 32 characters"`
	Tone string `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
	Icon string `json:"icon,omitempty" jsonschema:"one of the board icons"`
}
type BoardRow struct {
	ID    string      `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Cells []BoardCell `json:"cells" jsonschema:"one cell per column"`
}
type BoardTable struct {
	Columns []BoardColumn `json:"columns" jsonschema:"2-5 columns"`
	Rows    []BoardRow    `json:"rows" jsonschema:"1-10 rows"`
}
type BoardBar struct {
	ID      string  `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Label   string  `json:"label" jsonschema:"up to 24 characters"`
	Value   float64 `json:"value" jsonschema:"the length of the bar; not negative"`
	Display string  `json:"display,omitempty" jsonschema:"the figure as shown if it differs from the value, up to 12 characters"`
	Tone    string  `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BoardBars struct {
	Max  *float64   `json:"max,omitempty" jsonschema:"the value a full bar stands for; default the largest value"`
	Bars []BoardBar `json:"bars" jsonschema:"2-8 bars"`
}
type BoardPoint struct {
	Label string  `json:"label" jsonschema:"the x label, up to 8 characters"`
	Value float64 `json:"value"`
}
type BoardChart struct {
	Style  string       `json:"style" jsonschema:"line or column"`
	Unit   string       `json:"unit,omitempty" jsonschema:"up to 12 characters, shown with the last value"`
	Target *float64     `json:"target,omitempty" jsonschema:"draw a rule at this value"`
	Points []BoardPoint `json:"points" jsonschema:"2-24 points, in order. A snapshot: a series that keeps changing belongs in a tracker"`
	Tone   string       `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BoardProgress struct {
	ID    string  `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Label string  `json:"label" jsonschema:"up to 24 characters"`
	Value float64 `json:"value" jsonschema:"how far along; not negative"`
	Max   float64 `json:"max" jsonschema:"the full amount; above zero"`
	Note  string  `json:"note,omitempty" jsonschema:"up to 24 characters, such as 7 of 10"`
	Tone  string  `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BoardDetail struct {
	ID       string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Title    string `json:"title" jsonschema:"1-40 characters; shown folded"`
	Markdown string `json:"markdown" jsonschema:"authored Markdown, 1-4096 bytes; rendered without raw HTML"`
}
type BoardLink struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Title string `json:"title" jsonschema:"up to 60 characters"`
	URL   string `json:"url" jsonschema:"an http or https address to open"`
}

// BoardSection is one component. Kind names it, and only the field for that kind may be set.
type BoardSection struct {
	ID       string          `json:"id" jsonschema:"stable lowercase identifier, unique within the board"`
	Kind     string          `json:"kind" jsonschema:"stats, moves, watch, steps, callout, facts, table, bars, chart, progress, details or links"`
	Title    string          `json:"title,omitempty" jsonschema:"a small heading above the component, up to 40 characters; omit for the component's own default"`
	Stats    []BoardStat     `json:"stats,omitempty" jsonschema:"kind stats: 1-4 tiles"`
	Moves    []BoardMove     `json:"moves,omitempty" jsonschema:"kind moves: 1-8 things to do, in order"`
	Watch    []BoardWatch    `json:"watch,omitempty" jsonschema:"kind watch: 1-6 things that could change the plan"`
	Steps    []BoardStep     `json:"steps,omitempty" jsonschema:"kind steps: 1-8 upcoming steps, in order, drawn as a timeline"`
	Callout  *BoardCallout   `json:"callout,omitempty" jsonschema:"kind callout: one highlighted note"`
	Facts    []BoardFact     `json:"facts,omitempty" jsonschema:"kind facts: 1-8 label and value pairs"`
	Table    *BoardTable     `json:"table,omitempty" jsonschema:"kind table: a small comparison"`
	Bars     *BoardBars      `json:"bars,omitempty" jsonschema:"kind bars: values compared by length"`
	Chart    *BoardChart     `json:"chart,omitempty" jsonschema:"kind chart: a small line or column chart"`
	Progress []BoardProgress `json:"progress,omitempty" jsonschema:"kind progress: 1-6 bars toward a goal"`
	Details  []BoardDetail   `json:"details,omitempty" jsonschema:"kind details: 1-4 folded notes for method or reasoning"`
	Links    []BoardLink     `json:"links,omitempty" jsonschema:"kind links: 1-6 sources"`
}
type BoardParams struct {
	Eyebrow  string         `json:"eyebrow,omitempty" jsonschema:"a small line above the headline, up to 40 characters, such as GW12 · deadline Fri 18:30"`
	Headline string         `json:"headline,omitempty" jsonschema:"optional: a verdict or title in one line, up to 90 characters"`
	Subline  string         `json:"subline,omitempty" jsonschema:"one supporting line, up to 140 characters"`
	Tone     string         `json:"tone,omitempty" jsonschema:"colours the header: neutral (default), good, warn or risk"`
	Icon     string         `json:"icon,omitempty" jsonschema:"one of the board icons, drawn beside the headline"`
	Sections []BoardSection `json:"sections" jsonschema:"1-12 components in display order"`
}

// boardIcons is the one icon set every client draws (web with Lucide, Apple with SF Symbols).
var (
	boardIcons = map[string]bool{"": true, "check": true, "swap": true, "star": true, "alert": true, "clock": true, "calendar": true,
		"trend_up": true, "trend_down": true, "target": true, "shield": true, "bolt": true, "flag": true, "pin": true,
		"money": true, "user": true, "trophy": true, "heart": true}
	boardTones = map[string]bool{"": true, "neutral": true, "good": true, "warn": true, "risk": true}
	boardKinds = []string{"stats", "moves", "watch", "steps", "callout", "facts", "table", "bars", "chart", "progress", "details", "links"}
)

func checkBoard(raw json.RawMessage, _ []ArtifactSource) error {
	var p BoardParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return invalid("params: " + err.Error())
	}
	if len(p.Headline) > 90 || len(p.Eyebrow) > 40 || len(p.Subline) > 140 {
		return invalid("A board's headline is up to 90 characters, its eyebrow up to 40 and its subline up to 140")
	}
	if !boardTones[p.Tone] || !boardIcons[p.Icon] {
		return invalid("A board tone is neutral, good, warn or risk, and an icon is one of the board icons")
	}
	if len(p.Sections) < 1 || len(p.Sections) > 12 {
		return invalid("A board has 1-12 sections")
	}
	seen := map[string]bool{}
	unique := func(kind, id string) bool {
		if !artifactIdentifier.MatchString(id) || seen[kind+id] {
			return false
		}
		seen[kind+id] = true
		return true
	}
	for _, s := range p.Sections {
		if !unique("section", s.ID) || len(s.Title) > 40 {
			return invalid("A section has a unique id and a title up to 40 characters")
		}
		if err := checkBoardSection(s, unique); err != nil {
			return err
		}
	}
	return nil
}

func boardShort(s string, max int) bool { return strings.TrimSpace(s) != "" && len(s) <= max }
func boardNumber(v float64) bool        { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// have counts the component fields a section carries; only the one named by its kind may be set.
func boardFieldFor(s BoardSection) (set map[string]bool) {
	return map[string]bool{
		"stats": len(s.Stats) > 0, "moves": len(s.Moves) > 0, "watch": len(s.Watch) > 0, "steps": len(s.Steps) > 0, "callout": s.Callout != nil,
		"facts": len(s.Facts) > 0, "table": s.Table != nil, "bars": s.Bars != nil, "chart": s.Chart != nil, "progress": len(s.Progress) > 0,
		"details": len(s.Details) > 0, "links": len(s.Links) > 0,
	}
}

func checkBoardSection(s BoardSection, unique func(kind, id string) bool) error {
	known := false
	for _, k := range boardKinds {
		known = known || k == s.Kind
	}
	if !known {
		return invalid("Unknown section kind " + s.Kind + "; a board takes " + strings.Join(boardKinds, ", "))
	}
	for kind, set := range boardFieldFor(s) {
		if set && kind != s.Kind {
			return invalid("A " + s.Kind + " section takes only " + s.Kind + ", not " + kind)
		}
	}
	bad := func(what string) error { return invalid("A " + s.Kind + " section: " + what) }
	switch s.Kind {
	case "stats":
		if len(s.Stats) < 1 || len(s.Stats) > 4 {
			return bad("1-4 stats")
		}
		for _, x := range s.Stats {
			if !unique("stat", x.ID) || !boardShort(x.Label, 20) || !boardShort(x.Value, 12) || len(x.Note) > 24 || !boardIcons[x.Icon] || !boardTones[x.Tone] {
				return bad("each stat has a unique id, a label up to 20 characters, a value up to 12, a note up to 24, a board icon and a tone")
			}
		}
	case "moves":
		if len(s.Moves) < 1 || len(s.Moves) > 8 {
			return bad("1-8 moves")
		}
		for _, x := range s.Moves {
			if !unique("move", x.ID) || !boardShort(x.Title, 60) || len(x.Detail) > 80 || len(x.Tag) > 16 || !boardIcons[x.Icon] || !oneOf(x.State, "", "todo", "done", "hold") {
				return bad("each move has a unique id, a title up to 60 characters, a detail up to 80, a tag up to 16, a board icon and a state of todo, done or hold")
			}
		}
	case "watch":
		if len(s.Watch) < 1 || len(s.Watch) > 6 {
			return bad("1-6 watch items")
		}
		for _, x := range s.Watch {
			if !unique("watch", x.ID) || !boardShort(x.Title, 60) || len(x.Detail) > 80 || !boardIcons[x.Icon] || !oneOf(x.Level, "", "info", "warn", "risk") {
				return bad("each item has a unique id, a title up to 60 characters, a detail up to 80, a board icon and a level of info, warn or risk")
			}
		}
	case "steps":
		if len(s.Steps) < 1 || len(s.Steps) > 8 {
			return bad("1-8 steps")
		}
		for _, x := range s.Steps {
			if !unique("step", x.ID) || !boardShort(x.Label, 16) || !boardShort(x.Title, 48) || !boardIcons[x.Icon] || !boardTones[x.Tone] {
				return bad("each step has a unique id, a label up to 16 characters, a title up to 48, a board icon and a tone")
			}
		}
	case "callout":
		if c := s.Callout; c == nil || !boardShort(c.Text, 160) || len(c.Title) > 40 || !boardIcons[c.Icon] || !boardTones[c.Tone] {
			return bad("text up to 160 characters, a title up to 40, a board icon and a tone")
		}
	case "facts":
		if len(s.Facts) < 1 || len(s.Facts) > 8 {
			return bad("1-8 facts")
		}
		for _, x := range s.Facts {
			if !unique("fact", x.ID) || !boardShort(x.Label, 24) || !boardShort(x.Value, 40) || !boardIcons[x.Icon] {
				return bad("each fact has a unique id, a label up to 24 characters, a value up to 40 and a board icon")
			}
		}
	case "table":
		t := s.Table
		if t == nil || len(t.Columns) < 2 || len(t.Columns) > 5 || len(t.Rows) < 1 || len(t.Rows) > 10 {
			return bad("2-5 columns and 1-10 rows")
		}
		for _, c := range t.Columns {
			if !boardShort(c.Label, 16) || !oneOf(c.Align, "", "start", "end") {
				return bad("a column label is up to 16 characters and align is start or end")
			}
		}
		for _, r := range t.Rows {
			if !unique("row", r.ID) || len(r.Cells) != len(t.Columns) {
				return bad("each row has a unique id and one cell per column")
			}
			for _, c := range r.Cells {
				if len(c.Text) > 32 || !boardIcons[c.Icon] || !boardTones[c.Tone] {
					return bad("a cell is up to 32 characters with a board icon and a tone")
				}
			}
		}
	case "bars":
		b := s.Bars
		if b == nil || len(b.Bars) < 2 || len(b.Bars) > 8 || (b.Max != nil && (!boardNumber(*b.Max) || *b.Max <= 0)) {
			return bad("2-8 bars and, if given, a max above zero")
		}
		for _, x := range b.Bars {
			if !unique("bar", x.ID) || !boardShort(x.Label, 24) || !boardNumber(x.Value) || x.Value < 0 || len(x.Display) > 12 || !boardTones[x.Tone] {
				return bad("each bar has a unique id, a label up to 24 characters, a value of zero or more, a display up to 12 and a tone")
			}
		}
	case "chart":
		c := s.Chart
		if c == nil || !oneOf(c.Style, "line", "column") || len(c.Points) < 2 || len(c.Points) > 24 || len(c.Unit) > 12 || !boardTones[c.Tone] ||
			(c.Target != nil && !boardNumber(*c.Target)) {
			return bad("a style of line or column, 2-24 points, a unit up to 12 characters and a tone")
		}
		for _, pt := range c.Points {
			if len(pt.Label) > 8 || !boardNumber(pt.Value) {
				return bad("a point label is up to 8 characters and its value a number")
			}
		}
	case "progress":
		if len(s.Progress) < 1 || len(s.Progress) > 6 {
			return bad("1-6 progress bars")
		}
		for _, x := range s.Progress {
			if !unique("progress", x.ID) || !boardShort(x.Label, 24) || !boardNumber(x.Value) || x.Value < 0 || !boardNumber(x.Max) || x.Max <= 0 || len(x.Note) > 24 || !boardTones[x.Tone] {
				return bad("each has a unique id, a label up to 24 characters, a value of zero or more, a max above zero, a note up to 24 and a tone")
			}
		}
	case "details":
		if len(s.Details) < 1 || len(s.Details) > 4 {
			return bad("1-4 details")
		}
		for _, x := range s.Details {
			if !unique("detail", x.ID) || !boardShort(x.Title, 40) || strings.TrimSpace(x.Markdown) == "" || len(x.Markdown) > 4<<10 {
				return bad("each detail has a unique id, a title up to 40 characters and Markdown of at most 4096 bytes")
			}
		}
	case "links":
		if len(s.Links) < 1 || len(s.Links) > 6 {
			return bad("1-6 links")
		}
		for _, x := range s.Links {
			if !unique("link", x.ID) || !boardShort(x.Title, 60) {
				return bad("each link has a unique id and a title up to 60 characters")
			}
			if err := validateLink(x.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

const boardExample = `{"eyebrow":"GW12 · deadline Fri 18:30","headline":"Swap Raya for Trafford. Roll the other transfer.","subline":"Raya is 50% fit; Leeds have the best clean-sheet odds.","tone":"good","icon":"swap","sections":[
{"id":"numbers","kind":"stats","stats":[{"id":"gain","label":"5-week gain","value":"+12.9","note":"from one transfer","icon":"trend_up","tone":"good"},{"id":"rank","label":"League rank","value":"5th","note":"31 behind 2nd"},{"id":"ft","label":"Free transfers","value":"1","note":"rolled to next week","icon":"swap"}]},
{"id":"todo","kind":"moves","moves":[{"id":"gk","title":"Raya to Trafford","detail":"Raya 12.0 vs Trafford 24.9 points","icon":"swap","tag":"before Fri"},{"id":"cap","title":"Captain Groß","detail":"6.2 expected, Haaland 5.6 away","icon":"star"},{"id":"roll","title":"Keep the second transfer","state":"hold","icon":"clock"}]},
{"id":"goalkeepers","kind":"bars","title":"Goalkeepers, next 5 weeks","bars":{"bars":[{"id":"trafford","label":"Trafford","value":24.9,"tone":"good"},{"id":"tzolakis","label":"Tzolakis","value":23.9},{"id":"raya","label":"Raya","value":12,"tone":"risk"}]}},
{"id":"risks","kind":"watch","watch":[{"id":"raya","title":"Raya is 50% fit","level":"risk","icon":"heart"},{"id":"cards","title":"Three players on four yellows","detail":"A fifth is a ban","level":"warn","icon":"alert"}]},
{"id":"chips","kind":"steps","steps":[{"id":"tc","label":"GW15","title":"Triple Captain, Haaland at home","icon":"bolt","tone":"good"},{"id":"expiry","label":"GW19","title":"First chip set expires","icon":"calendar","tone":"warn"}]},
{"id":"how","kind":"details","details":[{"id":"method","title":"How this was scored","markdown":"Five gameweeks of fixtures, half underlying stats and half results."}]},
{"id":"sources","kind":"links","links":[{"id":"rotowire","title":"RotoWire GW12 rankings","url":"https://www.rotowire.com/soccer/"}]}]}`

func boardTemplate() *Template {
	raw, resolved := mustSchema[BoardParams]()
	return &Template{
		ID: "board.v1", Title: "Board",
		Description:  "A screen of typed components for any task: a plan, a weekly review, a comparison, a status, a dashboard-like summary. An optional header gives a title or a one-line verdict; then an ordered list of sections, each one component: stats (tiles), moves (things to do with a state), watch (risks), steps (a timeline), callout (one highlighted note), facts (label and value pairs), table (a small comparison), bars (values compared by length), chart (a small line or column snapshot), progress (bars toward a goal), details (folded Markdown for method) and links (sources). Compose and repeat them as the task needs; lead with what matters most. Every component is an authored snapshot: a value that keeps changing belongs in a tracker or collection. Keep every slot short: a long explanation belongs in a document or a details section. The icons are check, swap, star, alert, clock, calendar, trend_up, trend_down, target, shield, bolt, flag, pin, money, user, trophy and heart; the tones are neutral, good, warn and risk.",
		ParamsSchema: raw, Example: json.RawMessage(boardExample), Clients: []string{"web", "ios"}, schema: resolved, check: checkBoard,
	}
}
