package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const kitSubject = "8f2c1a0e-4b6d-4c9a-9f3e-2d7b5a1c6e40"

func loadKit(t *testing.T, name string) ViewData {
	t.Helper()
	raw, e := os.ReadFile("testdata/views/" + name + ".json")
	if e != nil {
		t.Fatal(e)
	}
	d := ViewData{SubjectKind: "goal", SubjectID: kitSubject, CatalogID: TrackerCatalogKit}
	if e = json.Unmarshal(raw, &d); e != nil {
		t.Fatalf("%s: %v", name, e)
	}
	return d
}

// The four goals of the design mock are the same kit composed four ways; none of them is a goal type.
func TestEveryExampleViewIsAcceptedAndComposedFromTheSameKit(t *testing.T) {
	for _, name := range []string{"dining_limit", "weight_target", "habit_days", "savings_deadline"} {
		if e := validateView(loadKit(t, name)); e != nil {
			t.Errorf("%s: %v", name, e)
		}
	}
}

func TestACatalogExampleIsAValidBlock(t *testing.T) {
	metric := func(id string) ViewMetric {
		return ViewMetric{TrackerMetric: TrackerMetric{ID: id, Label: id, Unit: "u"}}
	}
	metrics := map[string]ViewMetric{"spend": metric("spend"), "weight": metric("weight"), "stretch": metric("stretch"), "saved": metric("saved")}
	for _, b := range kitBlocks {
		var block ViewBlock
		if e := json.Unmarshal([]byte(b.Example), &block); e != nil {
			t.Fatalf("%s example: %v", b.Kind, e)
		}
		nodes := 0
		if e := validateBlock(block, metrics, &nodes); e != nil {
			t.Errorf("%s example is refused: %v", b.Kind, e)
		}
	}
}

func TestAManifestThatIsWrongIsRefusedWithAReason(t *testing.T) {
	bad := map[string]struct {
		edit func(*ViewData)
		want string
	}{
		"an unknown function":                    {func(d *ViewData) { d.Blocks[0].Slots["left"] = Slot{Calc: Calc{Fn: "magic"}, Format: "number"} }, "Unknown function"},
		"a metric that is not defined":           {func(d *ViewData) { d.Blocks[1].Value = &Calc{Fn: "latest", Metric: "nope"} }, "metric defined in this view"},
		"a field the kind does not take":         {func(d *ViewData) { d.Blocks[2].Layout = "month_grid" }, "does not take layout"},
		"a sentence slot nobody fills":           {func(d *ViewData) { d.Blocks[0].Text = "{left} left, {days} days, {extra}" }, "no slot"},
		"a slot the sentence never uses":         {func(d *ViewData) { d.Blocks[0].Text = "{left} left" }, "not used"},
		"a function with the wrong operands":     {func(d *ViewData) { d.Blocks[1].Of = &Calc{Fn: "sub", Args: []Calc{{Fn: "const", Value: ptr(1)}}} }, "takes 2-2 args"},
		"a parameter the function does not take": {func(d *ViewData) { d.Blocks[1].Of = &Calc{Fn: "const", Value: ptr(1), Period: "month"} }, "does not take period"},
		"days_left with both period and date":    {func(d *ViewData) { d.Blocks[1].Of = &Calc{Fn: "days_left", Period: "month", Date: "2026-12-31"} }, "either period or date"},
		"a made-up kind":                         {func(d *ViewData) { d.Blocks[0].Kind = "spending_limit" }, "Unknown block kind"},
		"a window with both period and days":     {func(d *ViewData) { d.Blocks[2].Window = &Window{Period: "month", Days: 7} }, "either period or days"},
	}
	for name, c := range bad {
		d := loadKit(t, "dining_limit")
		c.edit(&d)
		if e := validateView(d); e == nil || !strings.Contains(e.Error(), c.want) {
			t.Errorf("%s: wanted %q, got %v", name, c.want, e)
		}
	}
}

func ptr(f float64) *float64 { return &f }

func TestAnOverlyDeepOrLargeCalculationIsRefused(t *testing.T) {
	deep := Calc{Fn: "const", Value: ptr(1)}
	for i := 0; i < 6; i++ {
		deep = Calc{Fn: "add", Args: []Calc{deep, {Fn: "const", Value: ptr(1)}}}
	}
	d := loadKit(t, "dining_limit")
	d.Blocks[1].Of = &deep
	if e := validateView(d); e == nil || !strings.Contains(e.Error(), "five levels") {
		t.Fatalf("got %v", e)
	}
}

func TestStepsAndLogsAreHeldToTheirOwnRules(t *testing.T) {
	d := loadKit(t, "savings_deadline")
	d.Blocks[1].Thresholds = []float64{3000, 1000}
	if e := validateView(d); e == nil || !strings.Contains(e.Error(), "smallest first") {
		t.Fatalf("unsorted thresholds: %v", e)
	}
	d = loadKit(t, "dining_limit")
	d.Blocks = append(d.Blocks, ViewBlock{ID: "log", Kind: "log", Metric: "spend"})
	if e := validateView(d); e == nil || !strings.Contains(e.Error(), "data source") {
		t.Fatalf("a log on a sourced metric: %v", e)
	}
}
