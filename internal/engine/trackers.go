package engine

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// TrackerCatalogKit names the view kit. One manifest per goal serves every client: the kit is the same vocabulary
// everywhere and each client draws it with its own components, so a goal made on the web shows up on the phone.
const TrackerCatalogKit = "view-kit.v1"

// Data definitions are shared; each catalog describes a shipped native renderer.
type TrackerMetric struct {
	ID      string   `json:"id" jsonschema:"stable lowercase identifier, 1-64 characters; never change its unit after recording data"`
	Label   string   `json:"label" jsonschema:"human-readable measurement name, 1-80 characters"`
	Unit    string   `json:"unit" jsonschema:"measurement unit, 1-24 characters, e.g. kg, km, books"`
	Target  *float64 `json:"target,omitempty" jsonschema:"optional desired value; not a fabricated observation"`
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
}
type KitBlock struct {
	Kind    string   `json:"kind"`
	Purpose string   `json:"purpose"`
	Fields  []string `json:"fields"`
	Example string   `json:"example"`
}
type KitFunction struct {
	Fn     string   `json:"fn"`
	Takes  []string `json:"takes"`
	Result string   `json:"result"`
}
type TrackerCatalog struct {
	ID        string        `json:"id"`
	Blocks    []KitBlock    `json:"blocks"`
	Functions []KitFunction `json:"functions"`
	Formats   []string      `json:"formats"`
	Limits    string        `json:"limits"`
}
type TrackerCatalogInput struct {
	CatalogID string `json:"catalog_id,omitempty" jsonschema:"omit to list all supported client catalogs; otherwise request an exact id"`
}
type TrackerCatalogResult struct {
	Catalogs []TrackerCatalog `json:"catalogs"`
	Guidance string           `json:"guidance"`
}

// The kit is finite and describes ways to draw and calculate, never kinds of goal. Each block's purpose says what it
// shows; which goal needs which blocks is the composer's judgement.
var kitBlocks = []KitBlock{
	{"sentence", "One or two lines of plain words with numbers filled in from live data. The headline of a goal is usually a sentence.", []string{"text", "slots"},
		`{"id":"head","kind":"sentence","emphasis":"hero","text":"{left} left, {days} days to go","slots":{"left":{"calc":{"fn":"sub","args":[{"fn":"const","value":200},{"fn":"sum","metric":"spend","window":{"period":"month"}}]},"format":"money","unit":"USD"},"days":{"calc":{"fn":"days_left","period":"month"},"format":"number"}}}`},
	{"bar", "How far a value has got along a scale, with marks on the scale and a lighter extension for where it is heading.", []string{"value", "of", "markers", "projection", "format", "unit", "decimals"},
		`{"id":"pace","kind":"bar","value":{"fn":"sum","metric":"spend","window":{"period":"month"}},"of":{"fn":"const","value":200},"markers":[{"at":{"fn":"mul","args":[{"fn":"const","value":200},{"fn":"elapsed","period":"month"}]},"label":"even pace"}],"projection":{"fn":"project","metric":"spend","period":"month"},"format":"money","unit":"USD"}`},
	{"series", "One metric over time as a line, bars or dots, with optional reference lines, a shaded band, and a dashed line showing where the current rate leads.", []string{"metric", "style", "window", "bucket", "aggregate", "lines", "band", "trend_to", "format", "unit", "decimals"},
		`{"id":"trend","kind":"series","metric":"weight","style":"line","window":{"days":60},"band":{"from":{"fn":"const","value":77.5},"to":{"fn":"const","value":78.5},"label":"target"},"trend_to":{"fn":"const","value":78},"unit":"kg"}`},
	{"grid", "Which days something happened: one ring per day for a week, or a calendar of cells for several weeks.", []string{"metric", "layout", "weeks"},
		`{"id":"days","kind":"grid","metric":"stretch","layout":"month_grid","weeks":5}`},
	{"steps", "Either the goal's own one-off steps, or a ladder of thresholds a value climbs.", []string{"source", "thresholds", "value", "format", "unit"},
		`{"id":"ladder","kind":"steps","thresholds":[1000,2000,3000,4000,5000],"value":{"fn":"latest","metric":"saved"},"format":"money","unit":"USD"}`},
	{"timeline", "Dated marks on a line from today: a deadline, a projected finish.", []string{"markers"},
		`{"id":"when","kind":"timeline","markers":[{"at":{"fn":"date","date":"2026-12-31"},"label":"deadline"},{"at":{"fn":"eta","metric":"saved","args":[{"fn":"const","value":5000}]},"label":"at this pace"}]}`},
	{"routines", "The goal's own routines, each with a one-tap log for today, its streak, and a way into its detail. Use it when the person does something repeatedly for the goal.", []string{},
		`{"id":"habits","kind":"routines","title":"Your routines"}`},
	{"log", "A quick way for the person to record one number for a metric they log by hand.", []string{"metric"},
		`{"id":"log","kind":"log","metric":"weight"}`},
	{"text", "A few plain words that no data can supply.", []string{"body"}, `{"id":"why","kind":"text","body":"Ten minutes before coffee."}`},
	{"link", "A web page the person can open, for data the engine cannot chart.", []string{"url"}, `{"id":"src","kind":"link","url":"https://example.com"}`},
}

func trackerCatalog(id string) (TrackerCatalog, error) {
	if id != TrackerCatalogKit {
		return TrackerCatalog{}, invalid("Unsupported view kit; read tracker_catalog_get and use its catalog id")
	}
	fns := make([]KitFunction, 0, len(calcFns))
	for name, s := range calcFns {
		takes := []string{}
		for _, p := range []struct {
			on   bool
			name string
		}{{s.metric, "metric"}, {s.window, "window"}, {s.period, "period"}, {s.per, "per"}, {s.value, "value"}, {s.date, "date"}, {s.maxArgs > 0, "args"}} {
			if p.on {
				takes = append(takes, p.name)
			}
		}
		fns = append(fns, KitFunction{name, takes, s.doc})
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].Fn < fns[j].Fn })
	return TrackerCatalog{ID: id, Blocks: kitBlocks, Functions: fns,
		Formats: []string{"number", "money", "percent", "date", "days"},
		Limits:  "1-8 blocks; 0-6 metrics; a calculation nests five levels; a sentence has at most six slots."}, nil
}
func getTrackerCatalog(_ context.Context, _ *unit, in TrackerCatalogInput) (TrackerCatalogResult, error) {
	ids := []string{TrackerCatalogKit}
	if in.CatalogID != "" {
		ids = []string{in.CatalogID}
	}
	result := TrackerCatalogResult{Catalogs: []TrackerCatalog{}, Guidance: "Compose this goal's view from the kit, as a designer would for this one goal: lead with what the person most wants to know, in a sentence with live numbers; draw it the way its shape suggests; leave out whatever does not help. A metric is a data source: a provider series (source), a routine's logs (routine_id), or numbers the person logs (neither). The app does all arithmetic from live data, so never write a figure yourself: bind to a metric and use a function. If nothing in the kit fits, use text."}
	for _, id := range ids {
		c, e := trackerCatalog(id)
		if e != nil {
			return result, e
		}
		result.Catalogs = append(result.Catalogs, c)
	}
	return result, nil
}

var trackerIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// validateMetrics checks a definition's metrics and returns their ids.
func validateMetrics(ms []TrackerMetric) (map[string]bool, error) {
	metrics := map[string]bool{}
	for _, m := range ms {
		if !trackerIdentifier.MatchString(m.ID) || metrics[m.ID] {
			return nil, invalid("Metric ids must be unique lowercase identifiers")
		}
		metrics[m.ID] = true
		if strings.TrimSpace(m.Label) == "" || len(m.Label) > 80 || strings.TrimSpace(m.Unit) == "" || len(m.Unit) > 24 {
			return nil, invalid("Each metric needs a label (1-80 characters) and unit (1-24 characters)")
		}
		for _, p := range []*float64{m.Target, m.Minimum, m.Maximum} {
			if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0)) {
				return nil, invalid("Metric limits and targets must be finite")
			}
		}
		if m.Minimum != nil && m.Maximum != nil && *m.Minimum >= *m.Maximum {
			return nil, invalid("Metric minimum must be less than maximum")
		}
		if m.Target != nil {
			if e := measurementValue(m, *m.Target); e != nil {
				return nil, e
			}
		}
	}
	return metrics, nil
}
func measurementValue(m TrackerMetric, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return invalid("Measurement must be finite")
	}
	if m.Minimum != nil && value < *m.Minimum || m.Maximum != nil && value > *m.Maximum {
		return invalid("Measurement is outside the metric's allowed range")
	}
	return nil
}

type GoalMeasurement struct {
	ID         string     `json:"id"`
	GoalID     string     `json:"goal_id"`
	MetricID   string     `json:"metric_id"`
	Unit       string     `json:"unit"`
	Value      float64    `json:"value"`
	MeasuredAt time.Time  `json:"measured_at"`
	Notes      string     `json:"notes"`
	Version    int64      `json:"version"`
	DeletedAt  *time.Time `json:"deleted_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type MeasurementData struct {
	Value      float64 `json:"value"`
	MeasuredAt *string `json:"measured_at,omitempty" jsonschema:"RFC 3339 observation time; default now; must not be in the future"`
	Notes      string  `json:"notes,omitempty" jsonschema:"optional context, up to 1000 characters"`
}
type RecordMeasurementInput struct {
	Meta
	GoalID   string `json:"goal_id"`
	MetricID string `json:"metric_id"`
	MeasurementData
}
type UpdateMeasurementInput struct {
	EditInput
	MeasurementData
}

const measurementCols = `id::text,goal_id::text,metric_id,unit,value,measured_at,notes,version,deleted_at,created_at,updated_at`

func scanMeasurement(row scanner) (GoalMeasurement, error) {
	var m GoalMeasurement
	e := row.Scan(&m.ID, &m.GoalID, &m.MetricID, &m.Unit, &m.Value, &m.MeasuredAt, &m.Notes, &m.Version, &m.DeletedAt, &m.CreatedAt, &m.UpdatedAt)
	utcPtr(m.DeletedAt)
	m.MeasuredAt, m.CreatedAt, m.UpdatedAt = m.MeasuredAt.UTC(), m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	return m, e
}
func (u *unit) getMeasurement(ctx context.Context, key string, lock bool) (GoalMeasurement, error) {
	k, e := id(key)
	if e != nil {
		return GoalMeasurement{}, e
	}
	q := "SELECT " + measurementCols + " FROM life.goal_measurements WHERE id=$1"
	if lock {
		q += " FOR UPDATE"
	}
	return scanMeasurement(u.tx.QueryRow(ctx, q, k))
}

// metricFor finds a metric a goal's observations may be recorded against: one defined by a live view of the goal.
// Observations are kept per goal and metric id; a goal has one view, so a metric id has one unit.
func (u *unit) metricFor(ctx context.Context, g Goal, id string) (m TrackerMetric, err error) {
	if g.ArchivedAt != nil {
		return m, conflict("Restore the goal before recording progress")
	}
	rows, err := u.tx.Query(ctx, `SELECT metrics FROM life.views WHERE subject_kind='goal' AND subject_id=$1 AND deleted_at IS NULL ORDER BY catalog_id`, g.ID)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var metrics []ViewMetric
		if err = rows.Scan(&raw); err != nil {
			return m, err
		}
		if err = json.Unmarshal(raw, &metrics); err != nil {
			return m, err
		}
		for _, vm := range metrics {
			if vm.ID == id {
				return vm.TrackerMetric, nil
			}
		}
	}
	if err = rows.Err(); err != nil {
		return m, err
	}
	return m, invalid("Metric is not defined by any of this goal's views")
}
func (u *unit) measurementTime(ctx context.Context, d MeasurementData) (time.Time, error) {
	now, e := u.now(ctx)
	if e != nil {
		return now, e
	}
	at := now
	if len(d.Notes) > 1000 {
		return at, invalid("notes must be at most 1000 characters")
	}
	if d.MeasuredAt != nil {
		at, e = time.Parse(time.RFC3339Nano, *d.MeasuredAt)
		if e != nil {
			return at, invalid("measured_at must be RFC 3339")
		}
	}
	if at.After(now.Add(5 * time.Minute)) {
		return at, invalid("An observation cannot be in the future")
	}
	return at.UTC(), nil
}
func recordMeasurement(ctx context.Context, u *unit, in RecordMeasurementInput) (GoalMeasurement, error) {
	g, e := u.getGoal(ctx, in.GoalID, true)
	if e != nil {
		return GoalMeasurement{}, e
	}
	metric, e := u.metricFor(ctx, g, in.MetricID)
	if e != nil {
		return GoalMeasurement{}, e
	}
	if e = measurementValue(metric, in.Value); e != nil {
		return GoalMeasurement{}, e
	}
	at, e := u.measurementTime(ctx, in.MeasurementData)
	if e != nil {
		return GoalMeasurement{}, e
	}
	var key string
	e = u.tx.QueryRow(ctx, `INSERT INTO life.goal_measurements(goal_id,metric_id,unit,value,measured_at,notes) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, g.ID, metric.ID, metric.Unit, in.Value, at, in.Notes).Scan(&key)
	if e != nil {
		return GoalMeasurement{}, e
	}
	m, e := u.getMeasurement(ctx, key, false)
	if e != nil {
		return m, e
	}
	return m, u.audit(ctx, "goal_measurement", m.ID, nil, m)
}
func updateMeasurement(ctx context.Context, u *unit, in UpdateMeasurementInput) (GoalMeasurement, error) {
	before, e := u.getMeasurement(ctx, in.ID, false)
	if e != nil {
		return before, e
	}
	g, e := u.getGoal(ctx, before.GoalID, true)
	if e != nil {
		return before, e
	}
	before, e = u.getMeasurement(ctx, in.ID, true)
	if e != nil {
		return before, e
	}
	if e = version(in.ExpectedVersion, before.Version); e != nil {
		return before, e
	}
	if before.DeletedAt != nil {
		return before, conflict("Measurement was removed")
	}
	metric, e := u.metricFor(ctx, g, before.MetricID)
	if e != nil {
		return before, e
	}
	if e = measurementValue(metric, in.Value); e != nil {
		return before, e
	}
	data := in.MeasurementData
	if data.MeasuredAt == nil {
		v := before.MeasuredAt.Format(time.RFC3339Nano)
		data.MeasuredAt = &v
	}
	at, e := u.measurementTime(ctx, data)
	if e != nil {
		return before, e
	}
	if _, e = u.tx.Exec(ctx, "UPDATE life.goal_measurements SET value=$2,measured_at=$3,notes=$4,version=version+1,updated_at=now() WHERE id=$1", before.ID, in.Value, at, in.Notes); e != nil {
		return before, e
	}
	after, e := u.getMeasurement(ctx, before.ID, false)
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "goal_measurement", before.ID, before, after)
}
func deleteMeasurement(ctx context.Context, u *unit, in EditInput) (GoalMeasurement, error) {
	before, e := u.getMeasurement(ctx, in.ID, true)
	if e != nil {
		return before, e
	}
	if e = version(in.ExpectedVersion, before.Version); e != nil {
		return before, e
	}
	if before.DeletedAt != nil {
		return before, conflict("Measurement was already removed")
	}
	if _, e = u.tx.Exec(ctx, "UPDATE life.goal_measurements SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1", before.ID); e != nil {
		return before, e
	}
	after, e := u.getMeasurement(ctx, before.ID, false)
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "goal_measurement", before.ID, before, after)
}
