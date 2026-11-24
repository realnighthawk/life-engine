package engine

import (
	"encoding/json"
	"strings"
)

// Brief: a decision screen. A verdict first, then a few tiles, what to do, what to watch and what is ahead. It is
// drawn mostly with icons, tone and shape, so every text slot is short on purpose: a long explanation belongs in a
// document, or in a folded detail the person opens on purpose.

type BriefStat struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the brief"`
	Label string `json:"label" jsonschema:"what the number is, up to 20 characters"`
	Value string `json:"value" jsonschema:"the figure as shown, up to 12 characters, such as +12.9 or 5th"`
	Note  string `json:"note,omitempty" jsonschema:"a few words under the value, up to 24 characters"`
	Icon  string `json:"icon,omitempty" jsonschema:"one of the brief icons"`
	Tone  string `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BriefMove struct {
	ID     string `json:"id" jsonschema:"stable lowercase identifier, unique within the brief"`
	Title  string `json:"title" jsonschema:"the action, up to 60 characters"`
	Detail string `json:"detail,omitempty" jsonschema:"one short reason, up to 80 characters"`
	State  string `json:"state,omitempty" jsonschema:"todo (default), done or hold"`
	Icon   string `json:"icon,omitempty" jsonschema:"one of the brief icons"`
	Tag    string `json:"tag,omitempty" jsonschema:"a small label such as a deadline, up to 16 characters"`
}
type BriefWatch struct {
	ID     string `json:"id" jsonschema:"stable lowercase identifier, unique within the brief"`
	Title  string `json:"title" jsonschema:"the risk, up to 60 characters"`
	Detail string `json:"detail,omitempty" jsonschema:"one short note, up to 80 characters"`
	Level  string `json:"level,omitempty" jsonschema:"info, warn (default) or risk"`
	Icon   string `json:"icon,omitempty" jsonschema:"one of the brief icons"`
}
type BriefStep struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the brief"`
	Label string `json:"label" jsonschema:"when, up to 16 characters, such as GW15 or Jan"`
	Title string `json:"title" jsonschema:"what happens, up to 48 characters"`
	Icon  string `json:"icon,omitempty" jsonschema:"one of the brief icons"`
	Tone  string `json:"tone,omitempty" jsonschema:"neutral (default), good, warn or risk"`
}
type BriefDetail struct {
	ID       string `json:"id" jsonschema:"stable lowercase identifier, unique within the brief"`
	Title    string `json:"title" jsonschema:"1-40 characters; shown folded"`
	Markdown string `json:"markdown" jsonschema:"authored Markdown, 1-4096 bytes; rendered without raw HTML"`
}
type BriefLink struct {
	ID    string `json:"id" jsonschema:"stable lowercase identifier, unique within the brief"`
	Title string `json:"title" jsonschema:"up to 60 characters"`
	URL   string `json:"url" jsonschema:"an http or https address to open"`
}
type BriefParams struct {
	Eyebrow  string        `json:"eyebrow,omitempty" jsonschema:"a small line above the headline, up to 40 characters, such as GW12 · deadline Fri 18:30"`
	Headline string        `json:"headline" jsonschema:"the verdict in one line, 1-90 characters"`
	Subline  string        `json:"subline,omitempty" jsonschema:"one supporting line, up to 140 characters"`
	Tone     string        `json:"tone,omitempty" jsonschema:"colours the header: neutral (default), good, warn or risk"`
	Icon     string        `json:"icon,omitempty" jsonschema:"one of the brief icons, drawn beside the headline"`
	Stats    []BriefStat   `json:"stats,omitempty" jsonschema:"up to 4 tiles"`
	Moves    []BriefMove   `json:"moves,omitempty" jsonschema:"up to 8 things to do"`
	Watch    []BriefWatch  `json:"watch,omitempty" jsonschema:"up to 6 things that could change the plan"`
	Ahead    []BriefStep   `json:"ahead,omitempty" jsonschema:"up to 8 upcoming steps, in order"`
	Details  []BriefDetail `json:"details,omitempty" jsonschema:"up to 4 folded notes for the method or reasoning"`
	Links    []BriefLink   `json:"links,omitempty" jsonschema:"up to 6 sources"`
}

// briefIcons is the one icon set every client draws (web with Lucide, Apple with SF Symbols, Android with Material).
var (
	briefIcons = map[string]bool{"": true, "check": true, "swap": true, "star": true, "alert": true, "clock": true, "calendar": true,
		"trend_up": true, "trend_down": true, "target": true, "shield": true, "bolt": true, "flag": true, "pin": true,
		"money": true, "user": true, "trophy": true, "heart": true}
	briefTones = map[string]bool{"": true, "neutral": true, "good": true, "warn": true, "risk": true}
)

func checkBrief(raw json.RawMessage, _ []ArtifactSource) error {
	var p BriefParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return invalid("params: " + err.Error())
	}
	if n := len(strings.TrimSpace(p.Headline)); n < 1 || len(p.Headline) > 90 || len(p.Eyebrow) > 40 || len(p.Subline) > 140 {
		return invalid("A brief has a headline of 1-90 characters, an eyebrow up to 40 and a subline up to 140")
	}
	if !briefTones[p.Tone] || !briefIcons[p.Icon] {
		return invalid("A brief tone is neutral, good, warn or risk, and an icon is one of the brief icons")
	}
	if len(p.Stats) > 4 || len(p.Moves) > 8 || len(p.Watch) > 6 || len(p.Ahead) > 8 || len(p.Details) > 4 || len(p.Links) > 6 {
		return invalid("A brief has at most 4 stats, 8 moves, 6 watch items, 8 ahead steps, 4 details and 6 links")
	}
	if len(p.Stats)+len(p.Moves)+len(p.Watch)+len(p.Ahead) == 0 {
		return invalid("A brief needs at least one stat, move, watch item or ahead step; a headline alone is a text message")
	}
	seen := map[string]bool{}
	unique := func(kind, id string) bool {
		if !artifactIdentifier.MatchString(id) || seen[kind+id] {
			return false
		}
		seen[kind+id] = true
		return true
	}
	short := func(s string, max int) bool { return strings.TrimSpace(s) != "" && len(s) <= max }
	for _, s := range p.Stats {
		if !unique("stat", s.ID) || !short(s.Label, 20) || !short(s.Value, 12) || len(s.Note) > 24 || !briefIcons[s.Icon] || !briefTones[s.Tone] {
			return invalid("A stat has a unique id, a label up to 20 characters, a value up to 12, a note up to 24, a brief icon and a tone")
		}
	}
	for _, m := range p.Moves {
		if !unique("move", m.ID) || !short(m.Title, 60) || len(m.Detail) > 80 || len(m.Tag) > 16 || !briefIcons[m.Icon] || !oneOf(m.State, "", "todo", "done", "hold") {
			return invalid("A move has a unique id, a title up to 60 characters, a detail up to 80, a tag up to 16, a brief icon and a state of todo, done or hold")
		}
	}
	for _, w := range p.Watch {
		if !unique("watch", w.ID) || !short(w.Title, 60) || len(w.Detail) > 80 || !briefIcons[w.Icon] || !oneOf(w.Level, "", "info", "warn", "risk") {
			return invalid("A watch item has a unique id, a title up to 60 characters, a detail up to 80, a brief icon and a level of info, warn or risk")
		}
	}
	for _, s := range p.Ahead {
		if !unique("step", s.ID) || !short(s.Label, 16) || !short(s.Title, 48) || !briefIcons[s.Icon] || !briefTones[s.Tone] {
			return invalid("An ahead step has a unique id, a label up to 16 characters, a title up to 48, a brief icon and a tone")
		}
	}
	for _, d := range p.Details {
		if !unique("detail", d.ID) || !short(d.Title, 40) || strings.TrimSpace(d.Markdown) == "" || len(d.Markdown) > 4<<10 {
			return invalid("A detail has a unique id, a title up to 40 characters and Markdown of at most 4096 bytes")
		}
	}
	for _, l := range p.Links {
		if !unique("link", l.ID) || !short(l.Title, 60) {
			return invalid("A link has a unique id and a title up to 60 characters")
		}
		if err := validateLink(l.URL); err != nil {
			return err
		}
	}
	return nil
}

const briefExample = `{"eyebrow":"GW12 · deadline Fri 18:30","headline":"Swap Raya for Trafford. Roll the other transfer.","subline":"Raya is 50% fit; Leeds have the best clean-sheet odds.","tone":"good","icon":"swap","stats":[{"id":"gain","label":"5-week gain","value":"+12.9","note":"from one transfer","icon":"trend_up","tone":"good"},{"id":"rank","label":"League rank","value":"5th","note":"31 behind 2nd"},{"id":"ft","label":"Free transfers","value":"1","note":"rolled to next week","icon":"swap"}],"moves":[{"id":"gk","title":"Raya to Trafford","detail":"Raya 12.0 vs Trafford 24.9 points","icon":"swap","tag":"before Fri"},{"id":"cap","title":"Captain Groß","detail":"6.2 expected, Haaland 5.6 away","icon":"star"},{"id":"roll","title":"Keep the second transfer","state":"hold","icon":"clock"}],"watch":[{"id":"raya","title":"Raya is 50% fit","level":"risk","icon":"heart"},{"id":"cards","title":"Three players on four yellows","detail":"A fifth is a ban","level":"warn","icon":"alert"}],"ahead":[{"id":"tc","label":"GW15","title":"Triple Captain, Haaland at home","icon":"bolt","tone":"good"},{"id":"expiry","label":"GW19","title":"First chip set expires","icon":"calendar","tone":"warn"}],"details":[{"id":"method","title":"How this was scored","markdown":"Five gameweeks of fixtures, half underlying stats and half results."}],"links":[{"id":"rotowire","title":"RotoWire GW12 rankings","url":"https://www.rotowire.com/soccer/"}]}`

func briefTemplate() *Template {
	raw, resolved := mustSchema[BriefParams]()
	return &Template{
		ID: "brief.v1", Title: "Brief",
		Description:  "A decision screen drawn mostly with icons, tone and tiles: a one-line verdict, up to 4 stat tiles, the moves to make, what to watch, what is ahead, folded notes and sources. Choose it for a plan or a weekly decision the person acts on and returns to. Keep every slot short; a long explanation belongs in a document or a folded detail. The icons are check, swap, star, alert, clock, calendar, trend_up, trend_down, target, shield, bolt, flag, pin, money, user, trophy and heart.",
		ParamsSchema: raw, Example: json.RawMessage(briefExample), Clients: []string{"web", "ios"}, schema: resolved, check: checkBrief,
	}
}
