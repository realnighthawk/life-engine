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
type TrackerSection struct {
	ID       string `json:"id" jsonschema:"stable lowercase identifier, unique within the view"`
	Type     string `json:"type" jsonschema:"metric_trend, milestones, routine_consistency or check_in; only supported modules"`
	Title    string `json:"title" jsonschema:"short visible section heading, 1-80 characters"`
	MetricID string `json:"metric_id,omitempty" jsonschema:"required for metric_trend/check_in; must name a metric in this definition; absent for other types"`
}
type TrackerView struct {
	CatalogID string           `json:"catalog_id" jsonschema:"exact versioned catalog id returned by tracker_catalog_get; includes app and platform"`
	Sections  []TrackerSection `json:"sections" jsonschema:"1-8 modules in display order; at most four expanded initially on the phone"`
}
type TrackerDefinition struct {
	Metrics []TrackerMetric `json:"metrics" jsonschema:"0-6 shared numeric measurements; observations are recorded separately, never generated here"`
	Views   []TrackerView   `json:"views" jsonschema:"1-2 saved platform presentations, each with a different supported catalog_id; preserve existing presentations on revision"`
}

// Empty metrics must encode as an array in HTTP and MCP output schemas.
func (d TrackerDefinition) MarshalJSON() ([]byte, error) {
	type definition TrackerDefinition
	normalized := definition(d)
	if normalized.Metrics == nil {
		normalized.Metrics = []TrackerMetric{}
	}
	return json.Marshal(normalized)
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
		{"metric_trend", "A recorded outcome over time, with an optional target. Do not infer measurements from routine completion.", []string{"metric_id"}, "goal_measurements", []string{}},
		{"milestones", "One-off steps toward an outcome, in the goal's stored order.", []string{}, "goal.steps", []string{"steps_complete"}},
		{"routine_consistency", "How consistently the linked supporting behaviours are kept; not outcome progress.", []string{}, "goal.routines", []string{"routines_log", "open_routine"}},
		{"check_in", "Record or correct one numeric observation of a defined metric.", []string{"metric_id"}, "goal_measurements", []string{"goal_measurements_record", "goal_measurements_update", "goal_measurements_delete"}},
	}}, nil
}
func getTrackerCatalog(_ context.Context, _ *unit, in TrackerCatalogInput) (TrackerCatalogResult, error) {
	ids := []string{TrackerCatalogIOS, TrackerCatalogIPad}
	if in.CatalogID != "" {
		ids = []string{in.CatalogID}
	}
	result := TrackerCatalogResult{Catalogs: []TrackerCatalog{}, Guidance: "Choose what demonstrates this particular goal's outcome, not a generic dashboard. Reuse linked routines; distinguish consistency from outcomes. Compose only catalog modules with stable ids. Use the client's exact advertised catalog. Save the definition with goals_create for a new goal, or goals_propose_tracker for a revision. A proposal is not applied until the person accepts it. Preserve metrics and other client views when revising. The client owns fonts, colour, accessibility and responsive layout. No arbitrary code, queries, URLs or generated observations."}
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

func validateTracker(d TrackerDefinition) error {
	if len(d.Metrics) > 6 || len(d.Views) < 1 || len(d.Views) > 2 {
		return invalid("Tracker needs 0-6 metrics and 1-2 platform views")
	}
	metrics := map[string]bool{}
	for _, m := range d.Metrics {
		if !trackerIdentifier.MatchString(m.ID) || metrics[m.ID] {
			return invalid("Metric ids must be unique lowercase identifiers")
		}
		metrics[m.ID] = true
		if strings.TrimSpace(m.Label) == "" || len(m.Label) > 80 || strings.TrimSpace(m.Unit) == "" || len(m.Unit) > 24 {
			return invalid("Each metric needs a label (1-80 characters) and unit (1-24 characters)")
		}
		for _, p := range []*float64{m.Target, m.Minimum, m.Maximum} {
			if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0)) {
				return invalid("Metric limits and targets must be finite")
			}
		}
		if m.Minimum != nil && m.Maximum != nil && *m.Minimum >= *m.Maximum {
			return invalid("Metric minimum must be less than maximum")
		}
		if m.Target != nil {
			if e := measurementValue(m, *m.Target); e != nil {
				return e
			}
		}
	}
	catalogs := map[string]bool{}
	for _, v := range d.Views {
		if _, e := trackerCatalog(v.CatalogID); e != nil {
			return e
		}
		if catalogs[v.CatalogID] {
			return invalid("Only one presentation per catalog")
		}
		catalogs[v.CatalogID] = true
		if len(v.Sections) < 1 || len(v.Sections) > 8 {
			return invalid("Each tracker view needs 1-8 sections")
		}
		sections := map[string]bool{}
		for _, s := range v.Sections {
			if !trackerIdentifier.MatchString(s.ID) || sections[s.ID] || strings.TrimSpace(s.Title) == "" || len(s.Title) > 80 {
				return invalid("Section ids must be unique identifiers and headings must contain 1-80 characters")
			}
			sections[s.ID] = true
			switch s.Type {
			case "metric_trend", "check_in":
				if !metrics[s.MetricID] {
					return invalid("A measurement section must reference a defined metric")
				}
			case "milestones", "routine_consistency":
				if s.MetricID != "" {
					return invalid("Milestones and routine consistency cannot bind to a metric")
				}
			default:
				return invalid("Unknown tracker module; read the client's catalog")
			}
		}
	}
	return nil
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
func (u *unit) validateTrackerHistory(ctx context.Context, g Goal, d TrackerDefinition) error {
	if e := validateTracker(d); e != nil {
		return e
	}
	if g.Tracker != nil {
		for _, existing := range g.Tracker.Views {
			found := false
			for _, view := range d.Views {
				if view.CatalogID == existing.CatalogID {
					found = true
					break
				}
			}
			if !found {
				return conflict("Preserve the existing presentations for other clients when revising this tracker")
			}
		}
	}
	rows, e := u.tx.Query(ctx, "SELECT DISTINCT metric_id,unit FROM life.goal_measurements WHERE goal_id=$1", g.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, unit string
		if e = rows.Scan(&id, &unit); e != nil {
			return e
		}
		var found bool
		for _, m := range d.Metrics {
			if m.ID == id {
				found = true
				if m.Unit != unit {
					return conflict("Recorded metric units cannot change; create a new metric instead")
				}
			}
		}
		if !found {
			return conflict("A metric with recorded history must remain in the tracker definition")
		}
	}
	return rows.Err()
}

type TrackerProposal struct {
	ID                  string            `json:"id"`
	GoalID              string            `json:"goal_id"`
	BaseGoalVersion     int64             `json:"base_goal_version"`
	BaseTrackerRevision int64             `json:"base_tracker_revision"`
	Definition          TrackerDefinition `json:"definition"`
	Reason              string            `json:"reason"`
	State               string            `json:"state"`
	Version             int64             `json:"version"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}
type ProposeTrackerInput struct {
	EditInput
	Definition TrackerDefinition `json:"definition"`
	Reason     string            `json:"reason" jsonschema:"why this composition serves the goal, 1-1000 characters"`
}

const trackerProposalCols = `id::text,goal_id::text,base_goal_version,base_tracker_revision,definition,reason,state,version,created_at,updated_at`

func scanTrackerProposal(row scanner) (TrackerProposal, error) {
	var p TrackerProposal
	var raw []byte
	e := row.Scan(&p.ID, &p.GoalID, &p.BaseGoalVersion, &p.BaseTrackerRevision, &raw, &p.Reason, &p.State, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if e == nil {
		e = json.Unmarshal(raw, &p.Definition)
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, e
}
func (u *unit) trackerProposal(ctx context.Context, key string, lock bool) (TrackerProposal, error) {
	id, e := id(key)
	if e != nil {
		return TrackerProposal{}, e
	}
	q := "SELECT " + trackerProposalCols + " FROM life.tracker_proposals WHERE id=$1"
	if lock {
		q += " FOR UPDATE"
	}
	return scanTrackerProposal(u.tx.QueryRow(ctx, q, id))
}
func proposeTracker(ctx context.Context, u *unit, in ProposeTrackerInput) (TrackerProposal, error) {
	g, e := u.getGoal(ctx, in.ID, true)
	if e != nil {
		return TrackerProposal{}, e
	}
	if e = version(in.ExpectedVersion, g.Version); e != nil {
		return TrackerProposal{}, e
	}
	if g.ArchivedAt != nil {
		return TrackerProposal{}, conflict("Restore the goal before changing its tracker")
	}
	if e = u.validateTrackerHistory(ctx, g, in.Definition); e != nil {
		return TrackerProposal{}, e
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len(reason) > 1000 {
		return TrackerProposal{}, invalid("reason must contain 1-1000 characters")
	}
	var count int
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.tracker_proposals WHERE goal_id=$1 AND state='pending'", g.ID).Scan(&count); e != nil {
		return TrackerProposal{}, e
	}
	if count >= 10 {
		return TrackerProposal{}, conflict("Review or discard pending tracker proposals before adding another")
	}
	raw, _ := json.Marshal(in.Definition)
	var key string
	e = u.tx.QueryRow(ctx, `INSERT INTO life.tracker_proposals(goal_id,base_goal_version,base_tracker_revision,definition,reason) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, g.ID, g.Version, g.TrackerRevision, raw, reason).Scan(&key)
	if e != nil {
		return TrackerProposal{}, e
	}
	p, e := u.trackerProposal(ctx, key, false)
	if e != nil {
		return p, e
	}
	return p, u.audit(ctx, "tracker_proposal", p.ID, nil, p)
}
func applyTracker(ctx context.Context, u *unit, in EditInput) (Goal, error) {
	// All tracker edits take the goal lock first, then the proposal lock.
	p, e := u.trackerProposal(ctx, in.ID, false)
	if e != nil {
		return Goal{}, e
	}
	g, e := u.getGoal(ctx, p.GoalID, true)
	if e != nil {
		return Goal{}, e
	}
	p, e = u.trackerProposal(ctx, in.ID, true)
	if e != nil {
		return Goal{}, e
	}
	if e = version(in.ExpectedVersion, p.Version); e != nil {
		return Goal{}, e
	}
	if p.State != "pending" || g.ArchivedAt != nil {
		return Goal{}, conflict("This proposal is no longer pending or the goal is archived")
	}
	if p.BaseGoalVersion != g.Version || p.BaseTrackerRevision != g.TrackerRevision {
		return Goal{}, conflict("The goal or tracker changed; ask for a refreshed proposal")
	}
	if e = u.validateTrackerHistory(ctx, g, p.Definition); e != nil {
		return Goal{}, e
	}
	raw, _ := json.Marshal(p.Definition)
	if _, e = u.tx.Exec(ctx, "UPDATE life.goals SET tracker=$2,tracker_revision=tracker_revision+1,version=version+1,updated_at=now() WHERE id=$1", g.ID, raw); e != nil {
		return Goal{}, e
	}
	if _, e = u.tx.Exec(ctx, "UPDATE life.tracker_proposals SET state='applied',version=version+1,updated_at=now() WHERE id=$1", p.ID); e != nil {
		return Goal{}, e
	}
	after, e := u.getGoal(ctx, g.ID, false)
	if e != nil {
		return after, e
	}
	if e = u.audit(ctx, "goal", g.ID, g, after); e != nil {
		return after, e
	}
	pp, e := u.trackerProposal(ctx, p.ID, false)
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "tracker_proposal", p.ID, p, pp)
}
func discardTracker(ctx context.Context, u *unit, in EditInput) (TrackerProposal, error) {
	p, e := u.trackerProposal(ctx, in.ID, true)
	if e != nil {
		return p, e
	}
	if e = version(in.ExpectedVersion, p.Version); e != nil {
		return p, e
	}
	if p.State != "pending" {
		return p, conflict("Proposal is not pending")
	}
	if _, e = u.tx.Exec(ctx, "UPDATE life.tracker_proposals SET state='discarded',version=version+1,updated_at=now() WHERE id=$1", p.ID); e != nil {
		return p, e
	}
	after, e := u.trackerProposal(ctx, p.ID, false)
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "tracker_proposal", p.ID, p, after)
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
	GoalID                  string `json:"goal_id"`
	MetricID                string `json:"metric_id"`
	ExpectedTrackerRevision int64  `json:"expected_tracker_revision" jsonschema:"revision read from goals_tracker_get; rejects a changed metric definition"`
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
func measurementMetric(g Goal, id string) (TrackerMetric, error) {
	if g.ArchivedAt != nil {
		return TrackerMetric{}, conflict("Restore the goal before recording progress")
	}
	if g.Tracker != nil {
		for _, m := range g.Tracker.Metrics {
			if m.ID == id {
				return m, nil
			}
		}
	}
	return TrackerMetric{}, invalid("Metric is not defined in this goal's tracker")
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
	if e = version(in.ExpectedTrackerRevision, g.TrackerRevision); e != nil {
		return GoalMeasurement{}, e
	}
	metric, e := measurementMetric(g, in.MetricID)
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
	metric, e := measurementMetric(g, before.MetricID)
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

type TrackerGetInput struct {
	GoalID    string `json:"goal_id"`
	CatalogID string `json:"catalog_id" jsonschema:"exact catalog supported by the requesting client"`
}
type TrackerState struct {
	GoalID          string            `json:"goal_id"`
	GoalVersion     int64             `json:"goal_version"`
	TrackerRevision int64             `json:"tracker_revision"`
	CatalogID       string            `json:"catalog_id"`
	View            *TrackerView      `json:"view" jsonschema:"saved matching presentation, or null; never silently use another app's catalog"`
	Metrics         []TrackerMetric   `json:"metrics"`
	Measurements    []GoalMeasurement `json:"measurements" jsonschema:"most recent 120 observations per metric, ordered chronologically"`
	Proposals       []TrackerProposal `json:"proposals" jsonschema:"pending proposals that include this client's catalog"`
}

func getTracker(ctx context.Context, u *unit, in TrackerGetInput) (TrackerState, error) {
	result := TrackerState{CatalogID: in.CatalogID, Metrics: []TrackerMetric{}, Measurements: []GoalMeasurement{}, Proposals: []TrackerProposal{}}
	if _, e := trackerCatalog(in.CatalogID); e != nil {
		return result, e
	}
	g, e := u.getGoal(ctx, in.GoalID, false)
	if e != nil {
		return result, e
	}
	result.GoalID, result.GoalVersion, result.TrackerRevision = g.ID, g.Version, g.TrackerRevision
	if g.Tracker != nil {
		result.Metrics = append(result.Metrics, g.Tracker.Metrics...)
		for i := range g.Tracker.Views {
			if g.Tracker.Views[i].CatalogID == in.CatalogID {
				result.View = &g.Tracker.Views[i]
				break
			}
		}
	}
	rows, e := u.tx.Query(ctx, `SELECT `+measurementCols+` FROM life.goal_measurements WHERE id IN (
      SELECT id FROM (SELECT id,row_number() OVER(PARTITION BY metric_id ORDER BY measured_at DESC,id DESC) AS n FROM life.goal_measurements WHERE goal_id=$1 AND deleted_at IS NULL) recent WHERE n<=120)
      ORDER BY measured_at,id`, g.ID)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		m, e := scanMeasurement(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		result.Measurements = append(result.Measurements, m)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return result, e
	}
	rows, e = u.tx.Query(ctx, "SELECT "+trackerProposalCols+" FROM life.tracker_proposals WHERE goal_id=$1 AND state='pending' ORDER BY created_at DESC,id LIMIT 10", g.ID)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		p, e := scanTrackerProposal(rows)
		if e != nil {
			return result, e
		}
		for _, v := range p.Definition.Views {
			if v.CatalogID == in.CatalogID {
				result.Proposals = append(result.Proposals, p)
				break
			}
		}
	}
	return result, rows.Err()
}
