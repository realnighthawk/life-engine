package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// The view kit. A view is a manifest the model composes for one goal from a finite kit of ways to draw things (blocks)
// and a finite set of calculations the client runs on live data (functions). Nothing here names a kind of goal: a spending
// limit, a weigh-in and a habit are the same kit composed differently. The engine only checks that a manifest is well
// formed and refers to things that exist; the client does the arithmetic, so no number is ever written by the model.

const (
	maxCalcDepth = 5
	maxViewNodes = 400
)

type Window struct {
	Period string `json:"period,omitempty" jsonschema:"week or month: the current calendar week or month"`
	Days   int    `json:"days,omitempty" jsonschema:"the last N days, 1-366"`
}

// Calc is one calculation, a tree the client evaluates over live data. fn names a function from the catalog.
type Calc struct {
	Fn     string   `json:"fn" jsonschema:"a function from tracker_catalog_get"`
	Metric string   `json:"metric,omitempty" jsonschema:"the metric (a data source in this view) the function reads"`
	Value  *float64 `json:"value,omitempty" jsonschema:"const only: a number"`
	Date   string   `json:"date,omitempty" jsonschema:"date only: YYYY-MM-DD; days_left may use it instead of period"`
	Window *Window  `json:"window,omitempty" jsonschema:"sum, mean and count: which part of the data to use; omitted means all of it"`
	Period string   `json:"period,omitempty" jsonschema:"week or month, for days_left, elapsed and project"`
	Per    string   `json:"per,omitempty" jsonschema:"day, week or month, for rate"`
	Args   []Calc   `json:"args,omitempty" jsonschema:"the operands of add, sub, mul, div, min, max, and the target of eta"`
}

// Slot fills one {name} in a sentence: a calculation and how to print it.
type Slot struct {
	Calc     Calc   `json:"calc"`
	Format   string `json:"format" jsonschema:"number, money, percent, date or days"`
	Unit     string `json:"unit,omitempty" jsonschema:"a unit to print after a number (kg, km), or the currency for money (USD); at most 24 characters"`
	Decimals *int   `json:"decimals,omitempty" jsonschema:"0-3 digits after the point; omitted lets the client choose"`
}

type Marker struct {
	At    Calc   `json:"at" jsonschema:"where the marker sits: a number for bar and series, a date for timeline"`
	Label string `json:"label" jsonschema:"1-24 characters"`
}
type Band struct {
	From  Calc   `json:"from"`
	To    Calc   `json:"to"`
	Label string `json:"label" jsonschema:"1-24 characters"`
}

var (
	kitSlotName    = regexp.MustCompile(`\{([a-z][a-z0-9_]{0,31})\}`)
	kitPeriods     = map[string]bool{"week": true, "month": true}
	kitPers        = map[string]bool{"day": true, "week": true, "month": true}
	kitFormats     = map[string]bool{"number": true, "money": true, "percent": true, "date": true, "days": true}
	kitStyles      = map[string]bool{"line": true, "bars": true, "dots": true}
	kitBuckets     = map[string]bool{"day": true, "week": true, "month": true}
	kitAggregates  = map[string]bool{"sum": true, "mean": true, "last": true}
	kitLayouts     = map[string]bool{"week_row": true, "month_grid": true}
	kitEmphasis    = map[string]bool{"hero": true, "normal": true, "compact": true}
	kitBlockFields = map[string][]string{
		"sentence": {"text", "slots"},
		"bar":      {"value", "of", "markers", "projection", "format", "unit", "decimals"},
		"series":   {"metric", "style", "window", "bucket", "aggregate", "lines", "band", "trend_to", "format", "unit", "decimals"},
		"grid":     {"metric", "layout", "weeks"},
		"steps":    {"source", "thresholds", "value", "format", "unit"},
		"timeline": {"markers"},
		"routines": {},
		"log":      {"metric"},
		"text":     {"body", "points"},
		"link":     {"url"},
	}
)

// calcSpec says which parameters a function takes. A parameter not listed is refused, so a calculation means exactly
// one thing to the engine, the client and the model.
type calcSpec struct {
	metric, window, period, per, value, date bool
	minArgs, maxArgs                         int
	doc                                      string
}

var calcFns = map[string]calcSpec{
	"const":     {value: true, doc: "A fixed number."},
	"date":      {date: true, doc: "A fixed date."},
	"latest":    {metric: true, doc: "The newest value of the metric."},
	"first":     {metric: true, doc: "The oldest value of the metric."},
	"sum":       {metric: true, window: true, doc: "The values of the metric added up, over the window or all of it."},
	"mean":      {metric: true, window: true, doc: "The average of the metric's values, over the window or all of it."},
	"count":     {metric: true, window: true, doc: "How many days have a value above zero, over the window or all of it."},
	"add":       {minArgs: 2, maxArgs: 4, doc: "Add two to four calculations."},
	"mul":       {minArgs: 2, maxArgs: 4, doc: "Multiply two to four calculations."},
	"min":       {minArgs: 2, maxArgs: 4, doc: "The smallest of two to four calculations."},
	"max":       {minArgs: 2, maxArgs: 4, doc: "The largest of two to four calculations."},
	"sub":       {minArgs: 2, maxArgs: 2, doc: "args[0] minus args[1]."},
	"div":       {minArgs: 2, maxArgs: 2, doc: "args[0] divided by args[1]; unknown when args[1] is zero."},
	"days_left": {period: true, date: true, doc: "Whole days until the end of the current week or month (period), or until a date (date). Give exactly one."},
	"elapsed":   {period: true, doc: "How much of the current week or month has passed, from 0 to 1."},
	"project":   {metric: true, period: true, doc: "Where the metric's running total for the period is heading, at its pace so far."},
	"rate":      {metric: true, per: true, doc: "How fast the metric changes, per day, week or month, from its first to its latest value."},
	"eta":       {metric: true, minArgs: 1, maxArgs: 1, doc: "The date the metric reaches args[0] at its current rate; unknown when it is not heading there."},
	"streak":    {metric: true, doc: "Consecutive days up to today with a value above zero."},
}

func validateWindow(w *Window) error {
	if w == nil {
		return nil
	}
	if (w.Period == "") == (w.Days == 0) {
		return invalid("A window takes either period or days")
	}
	if w.Period != "" && !kitPeriods[w.Period] {
		return invalid("A window period is week or month")
	}
	if w.Days < 0 || w.Days > 366 {
		return invalid("A window of days is 1-366")
	}
	return nil
}

func validateCalc(c Calc, metrics map[string]bool, depth int, nodes *int) error {
	if depth > maxCalcDepth {
		return invalid("A calculation nests at most five levels")
	}
	if *nodes++; *nodes > maxViewNodes {
		return invalid("This view has too many calculations")
	}
	spec, ok := calcFns[c.Fn]
	if !ok {
		return invalid("Unknown function " + c.Fn + "; read tracker_catalog_get")
	}
	given := func(b bool, allowed bool, name string) error {
		if b && !allowed {
			return invalid("Function " + c.Fn + " does not take " + name)
		}
		return nil
	}
	for _, e := range []error{
		given(c.Metric != "", spec.metric, "metric"), given(c.Window != nil, spec.window, "window"),
		given(c.Period != "", spec.period, "period"), given(c.Per != "", spec.per, "per"),
		given(c.Value != nil, spec.value, "value"), given(c.Date != "", spec.date, "date"),
		given(len(c.Args) > 0, spec.maxArgs > 0, "args"),
	} {
		if e != nil {
			return e
		}
	}
	if spec.metric && !metrics[c.Metric] {
		return invalid("Function " + c.Fn + " must name a metric defined in this view")
	}
	if spec.value && c.Value == nil {
		return invalid("const needs a value")
	}
	if spec.date {
		if c.Fn == "days_left" {
			if (c.Date == "") == (c.Period == "") {
				return invalid("days_left takes either period or date")
			}
		} else if c.Date == "" {
			return invalid("date needs a date")
		}
		if c.Date != "" {
			if _, e := time.Parse("2006-01-02", c.Date); e != nil {
				return invalid("A date is written YYYY-MM-DD")
			}
		}
	}
	if spec.period && c.Fn != "days_left" && c.Period == "" {
		return invalid("Function " + c.Fn + " needs a period")
	}
	if c.Period != "" && !kitPeriods[c.Period] {
		return invalid("A period is week or month")
	}
	if spec.per && !kitPers[c.Per] {
		return invalid("rate needs per: day, week or month")
	}
	if e := validateWindow(c.Window); e != nil {
		return e
	}
	if len(c.Args) < spec.minArgs || len(c.Args) > spec.maxArgs {
		return invalid(fmt.Sprintf("Function %s takes %d-%d args", c.Fn, spec.minArgs, spec.maxArgs))
	}
	for _, a := range c.Args {
		if e := validateCalc(a, metrics, depth+1, nodes); e != nil {
			return e
		}
	}
	return nil
}

func validateSlot(s Slot, metrics map[string]bool, nodes *int) error {
	if !kitFormats[s.Format] {
		return invalid("A format is number, money, percent, date or days")
	}
	if len(s.Unit) > 24 || (s.Decimals != nil && (*s.Decimals < 0 || *s.Decimals > 3)) {
		return invalid("A unit is at most 24 characters and decimals are 0-3")
	}
	return validateCalc(s.Calc, metrics, 1, nodes)
}

func validateMarker(m Marker, metrics map[string]bool, nodes *int) error {
	if l := strings.TrimSpace(m.Label); l == "" || len(m.Label) > 24 {
		return invalid("A marker label is 1-24 characters")
	}
	return validateCalc(m.At, metrics, 1, nodes)
}

func validateSentence(text string, slots map[string]Slot, metrics map[string]bool, nodes *int) error {
	if strings.TrimSpace(text) == "" || len(text) > 160 {
		return invalid("A sentence is 1-160 characters")
	}
	used := map[string]bool{}
	for _, m := range kitSlotName.FindAllStringSubmatch(text, -1) {
		used[m[1]] = true
	}
	if len(slots) > 6 {
		return invalid("A sentence has at most six slots")
	}
	for name := range slots {
		if !used[name] {
			return invalid("Slot " + name + " is not used in the sentence")
		}
	}
	for name := range used {
		s, ok := slots[name]
		if !ok {
			return invalid("The sentence uses {" + name + "} but defines no slot for it")
		}
		if e := validateSlot(s, metrics, nodes); e != nil {
			return e
		}
	}
	return nil
}

// validateBlock checks one block against its kind: only that kind's fields, and each of them well formed.
func validateBlock(b ViewBlock, metrics map[string]ViewMetric, nodes *int) error {
	allowed, ok := kitBlockFields[b.Kind]
	if !ok {
		return invalid("Unknown block kind " + b.Kind + "; read tracker_catalog_get")
	}
	raw, _ := json.Marshal(b)
	var present map[string]json.RawMessage
	_ = json.Unmarshal(raw, &present)
	ok2 := map[string]bool{"id": true, "kind": true, "title": true, "emphasis": true}
	for _, f := range allowed {
		ok2[f] = true
	}
	for f := range present {
		if !ok2[f] {
			return invalid("A " + b.Kind + " block does not take " + f)
		}
	}
	if b.Emphasis != "" && !kitEmphasis[b.Emphasis] {
		return invalid("Emphasis is hero, normal or compact")
	}
	known := map[string]bool{}
	for id := range metrics {
		known[id] = true
	}
	needMetric := func() error {
		if !known[b.Metric] {
			return invalid("A " + b.Kind + " block must name a metric defined in this view")
		}
		return nil
	}
	calc := func(c *Calc, what string) error {
		if c == nil {
			return invalid("A " + b.Kind + " block needs " + what)
		}
		return validateCalc(*c, known, 1, nodes)
	}
	if b.Format != "" && !kitFormats[b.Format] || len(b.Unit) > 24 || (b.Decimals != nil && (*b.Decimals < 0 || *b.Decimals > 3)) {
		return invalid("Format is number, money, percent, date or days; unit is at most 24 characters; decimals 0-3")
	}
	switch b.Kind {
	case "sentence":
		return validateSentence(b.Text, b.Slots, known, nodes)
	case "bar":
		if e := calc(b.Value, "a value"); e != nil {
			return e
		}
		if e := calc(b.Of, "of, the full scale"); e != nil {
			return e
		}
		if b.Projection != nil {
			if e := validateCalc(*b.Projection, known, 1, nodes); e != nil {
				return e
			}
		}
		if len(b.Markers) > 3 {
			return invalid("A bar has at most three markers")
		}
		for _, m := range b.Markers {
			if e := validateMarker(m, known, nodes); e != nil {
				return e
			}
		}
	case "series":
		if e := needMetric(); e != nil {
			return e
		}
		if b.Style != "" && !kitStyles[b.Style] || b.Bucket != "" && !kitBuckets[b.Bucket] || b.Aggregate != "" && !kitAggregates[b.Aggregate] {
			return invalid("Series style is line, bars or dots; bucket is day, week or month; aggregate is sum, mean or last")
		}
		if e := validateWindow(b.Window); e != nil {
			return e
		}
		if len(b.Lines) > 3 {
			return invalid("A series has at most three reference lines")
		}
		for _, m := range b.Lines {
			if e := validateMarker(m, known, nodes); e != nil {
				return e
			}
		}
		if b.Band != nil {
			if l := strings.TrimSpace(b.Band.Label); l == "" || len(b.Band.Label) > 24 {
				return invalid("A band label is 1-24 characters")
			}
			if e := validateCalc(b.Band.From, known, 1, nodes); e != nil {
				return e
			}
			if e := validateCalc(b.Band.To, known, 1, nodes); e != nil {
				return e
			}
		}
		if b.TrendTo != nil {
			return validateCalc(*b.TrendTo, known, 1, nodes)
		}
	case "grid":
		if e := needMetric(); e != nil {
			return e
		}
		if !kitLayouts[b.Layout] || b.Weeks < 0 || b.Weeks > 12 {
			return invalid("A grid layout is week_row or month_grid, over 1-12 weeks")
		}
	case "steps":
		if (b.Source == "goal_steps") == (len(b.Thresholds) > 0) {
			return invalid("Steps take either source goal_steps or thresholds, not both")
		}
		if b.Source != "" && b.Source != "goal_steps" {
			return invalid("A steps source is goal_steps")
		}
		if len(b.Thresholds) > 0 {
			if len(b.Thresholds) < 2 || len(b.Thresholds) > 8 || !sort.Float64sAreSorted(b.Thresholds) {
				return invalid("Thresholds are 2-8 numbers, smallest first")
			}
			return calc(b.Value, "a value to compare with the thresholds")
		}
		if b.Value != nil {
			return invalid("goal_steps take no value")
		}
	case "timeline":
		if len(b.Markers) < 1 || len(b.Markers) > 4 {
			return invalid("A timeline has 1-4 markers")
		}
		for _, m := range b.Markers {
			if e := validateMarker(m, known, nodes); e != nil {
				return e
			}
		}
	case "log":
		if e := needMetric(); e != nil {
			return e
		}
		if m := metrics[b.Metric]; m.Source != nil || m.RoutineID != "" {
			return invalid("A log block records numbers by hand; the metric has a data source")
		}
	case "routines":
		// The goal's own routines: nothing to configure, so nothing to validate.
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
	case "link":
		return validateLink(b.URL)
	}
	return nil
}

// calcSchema is Calc's schema written by hand: a type that contains itself cannot be derived. It is kept small because
// every use repeats it in a tool's schema; the function list and the rules are in tracker_catalog_get, and the engine
// refuses a malformed calculation with the reason.
func calcSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Types:       []string{"object"},
		Description: "A calculation the client runs on live data: fn is a function from tracker_catalog_get, plus only the parameters that function takes. args hold more calculations (five levels at most).",
		Required:    []string{"fn"},
		Properties: map[string]*jsonschema.Schema{
			"fn":     {Types: []string{"string"}},
			"metric": {Types: []string{"string"}},
			"value":  {Types: []string{"number"}},
			"date":   {Types: []string{"string"}},
			"window": {Types: []string{"object"}},
			"period": {Types: []string{"string"}},
			"per":    {Types: []string{"string"}},
			"args":   {Types: []string{"array"}, Items: &jsonschema.Schema{Types: []string{"object"}}},
		},
	}
}
