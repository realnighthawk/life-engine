package engine

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"
)

const TrackerCatalogIOS = "mission-control.ios.goal-tracker.v1"
const TrackerCatalogIPad = "mission-control.ipados.goal-tracker.v1"

// Data definitions are shared; each catalog describes a shipped native renderer.
type TrackerMetric struct {
	ID      string   `json:"id" jsonschema:"stable lowercase identifier, 1-64 characters; never change its unit after recording data"`
	Label   string   `json:"label" jsonschema:"human-readable measurement name, 1-80 characters"`
	Unit    string   `json:"unit" jsonschema:"measurement unit, 1-24 characters, e.g. kg, km, books"`
	Target  *float64 `json:"target,omitempty" jsonschema:"optional desired value; not a fabricated observation"`
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
}
type TrackerModule struct {
	Type               string   `json:"type"`
	Purpose            string   `json:"purpose"`
	RequiredProperties []string `json:"required_properties"`
	DataSource         string   `json:"data_source"`
	Actions            []string `json:"actions"`
}
type TrackerCatalog struct {
	ID       string          `json:"id"`
	App      string          `json:"app"`
	Platform string          `json:"platform"`
	Layout   string          `json:"layout"`
	Modules  []TrackerModule `json:"modules"`
}
type TrackerCatalogInput struct {
	CatalogID string `json:"catalog_id,omitempty" jsonschema:"omit to list all supported client catalogs; otherwise request an exact id"`
}
type TrackerCatalogResult struct {
	Catalogs []TrackerCatalog `json:"catalogs"`
	Guidance string           `json:"guidance"`
}

func trackerCatalog(id string) (TrackerCatalog, error) {
	platform, layout := "ios", "stacked"
	switch id {
	case TrackerCatalogIOS:
	case TrackerCatalogIPad:
		platform, layout = "ipados", "adaptive"
	default:
		return TrackerCatalog{}, invalid("Unsupported tracker catalog; read tracker_catalog_get and use a catalog supported by the requesting app")
	}
	return TrackerCatalog{ID: id, App: "mission-control", Platform: platform, Layout: layout, Modules: []TrackerModule{
		{"metric_trend", "An outcome over time, with an optional target: recorded observations, or a provider series when the metric has a source. Do not infer measurements from routine completion.", []string{"metric_id"}, "goal_measurements or the metric's provider series", []string{}},
		{"milestones", "One-off steps toward an outcome, in the goal's stored order.", []string{}, "goal.steps", []string{"steps_complete"}},
		{"routine_consistency", "How consistently the linked supporting behaviours are kept; not outcome progress.", []string{}, "goal.routines", []string{"routines_log", "open_routine"}},
		{"check_in", "Record or correct one numeric observation of a defined metric.", []string{"metric_id"}, "goal_measurements", []string{"goal_measurements_record", "goal_measurements_update", "goal_measurements_delete"}},
		{"link", "A web page the person can open for data or detail the engine cannot chart.", []string{"url"}, "none", []string{"open_url"}},
	}}, nil
}
func getTrackerCatalog(_ context.Context, _ *unit, in TrackerCatalogInput) (TrackerCatalogResult, error) {
	ids := []string{TrackerCatalogIOS, TrackerCatalogIPad}
	if in.CatalogID != "" {
		ids = []string{in.CatalogID}
	}
	result := TrackerCatalogResult{Catalogs: []TrackerCatalog{}, Guidance: "Choose what demonstrates this particular goal's outcome, not a generic dashboard. Reuse linked routines; distinguish consistency from outcomes. Compose only catalog modules (blocks) with stable ids. Use the client's exact advertised catalog, or one view per catalog when none is advertised, with identical metrics. Save it as a view with views_create, as the last step of the batch_apply that creates the goal, and change it later with views_update. Keep a metric that has recorded observations, in its unit. A metric may name a provider series as its source; otherwise the person records it. The client owns fonts, colour, accessibility and responsive layout. No arbitrary code, queries or generated observations."}
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
// Observations are kept per goal and metric id, and metricsAgree keeps every view's unit for an id the same.
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
