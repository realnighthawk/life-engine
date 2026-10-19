package engine

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// A view is a manifest: blocks that refer to their subject and to metric definitions, never inlining the records they
// show. Native clients render the catalog's block types; anything else is refused here, so no markup or code is stored.

type MetricSource struct {
	Provider string `json:"provider" jsonschema:"the data engine that holds the series, a lowercase identifier such as health"`
	Series   string `json:"series" jsonschema:"a series that provider lists in its catalog, a lowercase identifier such as body_weight"`
}
type ViewMetric struct {
	TrackerMetric
	Source *MetricSource `json:"source,omitempty" jsonschema:"where the values come from. Leave it out when the person logs them by hand"`
}
type ViewBlock struct {
	ID       string `json:"id" jsonschema:"stable lowercase identifier, unique within the view"`
	Type     string `json:"type" jsonschema:"metric_trend, check_in, milestones, routine_consistency or link"`
	Title    string `json:"title" jsonschema:"short visible heading, 1-80 characters"`
	MetricID string `json:"metric_id,omitempty" jsonschema:"required for metric_trend and check_in; must name a metric in this view; absent otherwise"`
	URL      string `json:"url,omitempty" jsonschema:"required for link: an http or https address to open; absent otherwise"`
}
type ViewData struct {
	SubjectKind string       `json:"subject_kind" jsonschema:"what the view is of: goal"`
	SubjectID   string       `json:"subject_id" jsonschema:"UUID of that record; in a batch it may be a record an earlier step creates"`
	CatalogID   string       `json:"catalog_id" jsonschema:"exact versioned catalog id from tracker_catalog_get; names the app and platform that renders it"`
	Metrics     []ViewMetric `json:"metrics" jsonschema:"0-6 measurements the blocks refer to; observations are recorded separately, never generated here"`
	Blocks      []ViewBlock  `json:"blocks" jsonschema:"1-8 blocks in display order"`
}
type CreateViewInput struct {
	Meta
	ID string `json:"id,omitempty" jsonschema:"optional client-generated UUID, so a plan can refer to the view before it exists"`
	ViewData
}
type View struct {
	ID string `json:"id"`
	ViewData
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type UpdateViewInput struct {
	EditInput
	Metrics []ViewMetric `json:"metrics" jsonschema:"the whole list, replacing the current one"`
	Blocks  []ViewBlock  `json:"blocks" jsonschema:"the whole list, replacing the current one"`
}
type ViewsListInput struct {
	SubjectKind string `json:"subject_kind" jsonschema:"goal"`
	SubjectID   string `json:"subject_id"`
	CatalogID   string `json:"catalog_id,omitempty" jsonschema:"only the view for this client catalog"`
}
type ViewList struct {
	Items []View `json:"items"`
}

func validateView(d ViewData) error {
	if d.SubjectKind != "goal" {
		return invalid("subject_kind must be goal")
	}
	if _, e := id(d.SubjectID); e != nil {
		return invalid("subject_id must be a UUID")
	}
	if _, e := trackerCatalog(d.CatalogID); e != nil {
		return e
	}
	if len(d.Metrics) > 6 || len(d.Blocks) < 1 || len(d.Blocks) > 8 {
		return invalid("A view needs 0-6 metrics and 1-8 blocks")
	}
	plain := make([]TrackerMetric, len(d.Metrics))
	for i, m := range d.Metrics {
		plain[i] = m.TrackerMetric
		if m.Source != nil && (!trackerIdentifier.MatchString(m.Source.Provider) || !trackerIdentifier.MatchString(m.Source.Series)) {
			return invalid("A metric source needs a lowercase provider and series identifier")
		}
	}
	metrics, e := validateMetrics(plain)
	if e != nil {
		return e
	}
	blocks := map[string]bool{}
	for _, b := range d.Blocks {
		if !trackerIdentifier.MatchString(b.ID) || blocks[b.ID] || strings.TrimSpace(b.Title) == "" || len(b.Title) > 80 {
			return invalid("Block ids must be unique identifiers and headings must contain 1-80 characters")
		}
		blocks[b.ID] = true
		switch b.Type {
		case "metric_trend", "check_in":
			if !metrics[b.MetricID] || b.URL != "" {
				return invalid("A measurement block must reference a defined metric and take no url")
			}
		case "milestones", "routine_consistency":
			if b.MetricID != "" || b.URL != "" {
				return invalid("Milestones and routine consistency take neither a metric nor a url")
			}
		case "link":
			if u, e := url.Parse(b.URL); e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(b.URL) > 2048 || b.MetricID != "" {
				return invalid("A link block needs an http or https url and no metric")
			}
		default:
			return invalid("Unknown block type; read the client's catalog")
		}
	}
	return nil
}

const viewCols = `id::text,subject_kind,subject_id::text,catalog_id,metrics,blocks,version,created_at,updated_at`

func scanView(row scanner) (View, error) {
	var v View
	var metrics, blocks []byte
	if e := row.Scan(&v.ID, &v.SubjectKind, &v.SubjectID, &v.CatalogID, &metrics, &blocks, &v.Version, &v.CreatedAt, &v.UpdatedAt); e != nil {
		return v, e
	}
	v.Metrics, v.Blocks = []ViewMetric{}, []ViewBlock{}
	if e := json.Unmarshal(metrics, &v.Metrics); e != nil {
		return v, e
	}
	return v, json.Unmarshal(blocks, &v.Blocks)
}

func (u *unit) getView(ctx context.Context, key string) (View, error) {
	k, e := id(key)
	if e != nil {
		return View{}, e
	}
	return scanView(u.tx.QueryRow(ctx, `SELECT `+viewCols+` FROM life.views WHERE id=$1 AND deleted_at IS NULL`, k))
}

func createView(ctx context.Context, u *unit, in CreateViewInput) (View, error) {
	key, e := createID(in.ID)
	if e != nil {
		return View{}, e
	}
	if e = validateView(in.ViewData); e != nil {
		return View{}, e
	}
	found, e := u.exists(ctx, in.SubjectKind, in.SubjectID)
	if e != nil {
		return View{}, e
	}
	if !found {
		return View{}, invalid("The view's subject is not a " + in.SubjectKind + " that exists")
	}
	if e = u.metricsAgree(ctx, in.SubjectKind, in.SubjectID, "", in.Metrics); e != nil {
		return View{}, e
	}
	var taken bool
	if e = u.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM life.views WHERE subject_kind=$1 AND subject_id=$2 AND catalog_id=$3 AND deleted_at IS NULL)`,
		in.SubjectKind, in.SubjectID, in.CatalogID).Scan(&taken); e != nil {
		return View{}, e
	}
	if taken {
		return View{}, conflict("This subject already has a view for that catalog")
	}
	metrics, _ := json.Marshal(in.Metrics)
	blocks, _ := json.Marshal(in.Blocks)
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.views(id,subject_kind,subject_id,catalog_id,metrics,blocks) VALUES($1,$2,$3,$4,$5,$6)`,
		key, in.SubjectKind, in.SubjectID, in.CatalogID, metrics, blocks); e != nil {
		return View{}, e
	}
	v, e := u.getView(ctx, key)
	if e != nil {
		return v, e
	}
	return v, u.audit(ctx, "view", v.ID, nil, v)
}

func getView(ctx context.Context, u *unit, in GetInput) (View, error) {
	return u.getView(ctx, in.ID)
}

func listViews(ctx context.Context, u *unit, in ViewsListInput) (ViewList, error) {
	out := ViewList{Items: []View{}}
	if in.SubjectKind != "goal" {
		return out, invalid("subject_kind must be goal")
	}
	subject, e := id(in.SubjectID)
	if e != nil {
		return out, invalid("subject_id must be a UUID")
	}
	rows, e := u.tx.Query(ctx, `SELECT `+viewCols+` FROM life.views WHERE subject_kind=$1 AND subject_id=$2 AND ($3='' OR catalog_id=$3) AND deleted_at IS NULL ORDER BY catalog_id`,
		in.SubjectKind, subject, in.CatalogID)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanView(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}

// metricsAgree refuses a metric whose unit differs from the same metric id in the subject's other live views or in
// the same goal: observations are kept per goal and metric id, so every definition of one id has to mean the same thing.
func (u *unit) metricsAgree(ctx context.Context, kind, subject, except string, metrics []ViewMetric) error {
	units := map[string]string{}
	rows, e := u.tx.Query(ctx, `SELECT metrics FROM life.views WHERE subject_kind=$1 AND subject_id=$2 AND deleted_at IS NULL AND id::text<>$3`, kind, subject, except)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var others []ViewMetric
		if e = rows.Scan(&raw); e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &others); e != nil {
			return e
		}
		for _, m := range others {
			units[m.ID] = m.Unit
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for _, m := range metrics {
		if unit, ok := units[m.ID]; ok && unit != m.Unit {
			return conflict("Metric " + m.ID + " is already defined in " + unit + " for this goal; keep the unit, or use a new metric id")
		}
	}
	return nil
}

func updateView(ctx context.Context, u *unit, in UpdateViewInput) (View, error) {
	k, e := id(in.ID)
	if e != nil {
		return View{}, e
	}
	var deleted bool
	if e = u.tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM life.views WHERE id=$1 FOR UPDATE`, k).Scan(&deleted); e != nil {
		return View{}, e
	}
	if deleted {
		return View{}, conflict("The view was removed")
	}
	old, e := u.getView(ctx, k)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	next := old.ViewData
	next.Metrics, next.Blocks = in.Metrics, in.Blocks
	if e = validateView(next); e != nil {
		return old, e
	}
	if e = u.metricsAgree(ctx, old.SubjectKind, old.SubjectID, old.ID, in.Metrics); e != nil {
		return old, e
	}
	if e = u.viewKeepsRecordedMetrics(ctx, old, in.Metrics); e != nil {
		return old, e
	}
	metrics, _ := json.Marshal(in.Metrics)
	blocks, _ := json.Marshal(in.Blocks)
	if _, e = u.tx.Exec(ctx, `UPDATE life.views SET metrics=$2,blocks=$3,version=version+1,updated_at=now() WHERE id=$1`, old.ID, metrics, blocks); e != nil {
		return old, e
	}
	after, e := u.getView(ctx, old.ID)
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "view", after.ID, old, after)
}

// viewKeepsRecordedMetrics holds the tracker rule for views: a metric that has observations stays, in its unit.
func (u *unit) viewKeepsRecordedMetrics(ctx context.Context, old View, metrics []ViewMetric) error {
	rows, e := u.tx.Query(ctx, `SELECT DISTINCT metric_id,unit FROM life.goal_measurements WHERE goal_id=$1`, old.SubjectID)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var metric, unit string
		if e = rows.Scan(&metric, &unit); e != nil {
			return e
		}
		for _, was := range old.Metrics {
			if was.ID != metric {
				continue
			}
			found := false
			for _, m := range metrics {
				if m.ID == metric {
					found = true
					if m.Unit != unit {
						return conflict("Recorded metric units cannot change; create a new metric instead")
					}
				}
			}
			if !found {
				return conflict("A metric with recorded history must remain in the view")
			}
		}
	}
	return rows.Err()
}

func deleteView(ctx context.Context, u *unit, in EditInput) (View, error) {
	k, e := id(in.ID)
	if e != nil {
		return View{}, e
	}
	var deleted bool
	if e = u.tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM life.views WHERE id=$1 FOR UPDATE`, k).Scan(&deleted); e != nil {
		return View{}, e
	}
	if deleted {
		return View{}, conflict("The view was already removed")
	}
	old, e := u.getView(ctx, k)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.views SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	// A removed view has no deleted_at in the live query, so read its row directly for the audit trail.
	after, e := scanView(u.tx.QueryRow(ctx, `SELECT `+viewCols+` FROM life.views WHERE id=$1`, old.ID))
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "view", after.ID, old, after)
}

type ViewStateInput struct {
	GoalID    string `json:"goal_id"`
	CatalogID string `json:"catalog_id" jsonschema:"exact catalog supported by the requesting client"`
}
type ViewState struct {
	GoalID       string            `json:"goal_id"`
	CatalogID    string            `json:"catalog_id"`
	View         *View             `json:"view" jsonschema:"the goal's view for this catalog, or null; never silently use another app's catalog"`
	Measurements []GoalMeasurement `json:"measurements" jsonschema:"the latest 120 recorded observations per metric, oldest first"`
}

// viewState is what a client opens a goal's tracker with: its view for the client's catalog and the observations
// recorded against the goal's metrics.
func viewState(ctx context.Context, u *unit, in ViewStateInput) (ViewState, error) {
	out := ViewState{CatalogID: in.CatalogID, Measurements: []GoalMeasurement{}}
	if _, e := trackerCatalog(in.CatalogID); e != nil {
		return out, e
	}
	g, e := u.getGoal(ctx, in.GoalID, false)
	if e != nil {
		return out, e
	}
	out.GoalID = g.ID
	views, e := listViews(ctx, u, ViewsListInput{SubjectKind: "goal", SubjectID: g.ID, CatalogID: in.CatalogID})
	if e != nil {
		return out, e
	}
	if len(views.Items) > 0 {
		out.View = &views.Items[0]
	}
	rows, e := u.tx.Query(ctx, `SELECT `+measurementCols+` FROM life.goal_measurements WHERE id IN (
      SELECT id FROM (SELECT id,row_number() OVER(PARTITION BY metric_id ORDER BY measured_at DESC,id DESC) AS n FROM life.goal_measurements WHERE goal_id=$1 AND deleted_at IS NULL) recent WHERE n<=120)
      ORDER BY measured_at,id`, g.ID)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		m, e := scanMeasurement(rows)
		if e != nil {
			return out, e
		}
		out.Measurements = append(out.Measurements, m)
	}
	return out, rows.Err()
}
