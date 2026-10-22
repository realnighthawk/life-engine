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
	Provider    string `json:"provider" jsonschema:"the data engine that holds the series, a lowercase identifier such as health"`
	Series      string `json:"series" jsonschema:"a series that provider lists in its catalog, a lowercase identifier such as body_weight"`
	Granularity string `json:"granularity,omitempty" jsonschema:"day, week or month: the resolution the client asks the provider for; omitted means day"`
}
type ViewMetric struct {
	TrackerMetric
	Source *MetricSource `json:"source,omitempty" jsonschema:"a data provider's series the values come from, read live. Leave both source and task_id out when the person logs the values by hand"`
	TaskID string        `json:"task_id,omitempty" jsonschema:"a repeating task of this person whose done occurrences are the values (one per day done); instead of source"`
}

// ViewBlock is one block of the kit. Which fields a kind takes is in tracker_catalog_get; a field a kind does not take
// is refused. The fields are flat so the schema stays one shape, not a list of goal kinds.
type ViewBlock struct {
	ID       string `json:"id" jsonschema:"stable lowercase identifier, unique within the view"`
	Kind     string `json:"kind" jsonschema:"sentence, bar, series, grid, steps, timeline, tasks, events, log, text or link"`
	Title    string `json:"title,omitempty" jsonschema:"optional short heading, up to 80 characters"`
	Emphasis string `json:"emphasis,omitempty" jsonschema:"hero, normal or compact: how much room the client gives it"`
	// sentence
	Text  string          `json:"text,omitempty" jsonschema:"sentence: the words, with {name} where a number goes, up to 160 characters"`
	Slots map[string]Slot `json:"slots,omitempty" jsonschema:"sentence: for each {name}, the calculation that fills it and how to print it"`
	// bar
	Value      *Calc    `json:"value,omitempty" jsonschema:"bar: how far along; steps with thresholds: the number compared with them"`
	Of         *Calc    `json:"of,omitempty" jsonschema:"bar: the full scale (a limit or a target)"`
	Markers    []Marker `json:"markers,omitempty" jsonschema:"bar: up to three marks on the scale; timeline: 1-4 dated marks"`
	Projection *Calc    `json:"projection,omitempty" jsonschema:"bar: where the value is heading, drawn as a lighter extension"`
	// series, grid, log
	Metric    string   `json:"metric,omitempty" jsonschema:"series, grid, log: the metric the block draws or records"`
	Style     string   `json:"style,omitempty" jsonschema:"series: line, bars or dots"`
	Window    *Window  `json:"window,omitempty" jsonschema:"series: the part of the data to show"`
	Bucket    string   `json:"bucket,omitempty" jsonschema:"series: group values by day, week or month"`
	Aggregate string   `json:"aggregate,omitempty" jsonschema:"series: how a group becomes one value: sum, mean or last"`
	Lines     []Marker `json:"lines,omitempty" jsonschema:"series: up to three horizontal reference lines (a limit, a target)"`
	Band      *Band    `json:"band,omitempty" jsonschema:"series: a shaded range (a target zone)"`
	TrendTo   *Calc    `json:"trend_to,omitempty" jsonschema:"series: draw the current rate as a dashed line until it reaches this value"`
	Layout    string   `json:"layout,omitempty" jsonschema:"grid: week_row or month_grid"`
	Weeks     int      `json:"weeks,omitempty" jsonschema:"grid: how many weeks, 1-12"`
	// steps
	Thresholds []float64 `json:"thresholds,omitempty" jsonschema:"steps: 2-8 numbers, smallest first, drawn as a ladder the value climbs"`
	// formatting for bar, series, steps
	Format   string `json:"format,omitempty" jsonschema:"number, money, percent, date or days"`
	Unit     string `json:"unit,omitempty" jsonschema:"a unit or currency to print, up to 24 characters"`
	Decimals *int   `json:"decimals,omitempty" jsonschema:"0-3"`
	// text, link
	Body   string   `json:"body,omitempty" jsonschema:"text: plain words, up to 280 characters"`
	Points []string `json:"points,omitempty" jsonschema:"text: up to five short points, each up to 120 characters"`
	URL    string   `json:"url,omitempty" jsonschema:"link: an http or https address to open"`
}

// A view is organised as pages of sections of blocks. The structure is mandatory; what goes in it is the composer's call.
type ViewSection struct {
	ID     string      `json:"id" jsonschema:"stable lowercase identifier, unique within the view"`
	Title  string      `json:"title" jsonschema:"what this part of the goal is about, 1-60 characters"`
	Intro  string      `json:"intro,omitempty" jsonschema:"optional one or two plain sentences that say why it matters or how to read it, up to 240 characters; never a figure"`
	Blocks []ViewBlock `json:"blocks" jsonschema:"1-8 blocks in display order"`
}
type ViewPage struct {
	ID       string        `json:"id" jsonschema:"stable lowercase identifier, unique within the view"`
	Title    string        `json:"title,omitempty" jsonschema:"tab name, 1-24 characters; required when the view has more than one page"`
	Sections []ViewSection `json:"sections" jsonschema:"1-6 sections in display order"`
}
type ViewData struct {
	SubjectKind string       `json:"subject_kind" jsonschema:"what the view is of: goal, task or event"`
	SubjectID   string       `json:"subject_id" jsonschema:"UUID of that record; in a batch it may be a record an earlier step creates"`
	CatalogID   string       `json:"catalog_id" jsonschema:"exact versioned catalog id from tracker_catalog_get; names the app and platform that renders it"`
	Metrics     []ViewMetric `json:"metrics" jsonschema:"0-6 measurements the blocks refer to; observations are recorded separately, never generated here"`
	Pages       []ViewPage   `json:"pages" jsonschema:"1-4 pages; one page shows no tabs"`
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
	Pages   []ViewPage   `json:"pages" jsonschema:"the whole structure, replacing the current one"`
}
type ViewsListInput struct {
	SubjectKind string `json:"subject_kind" jsonschema:"goal, task or event"`
	SubjectID   string `json:"subject_id"`
	CatalogID   string `json:"catalog_id,omitempty" jsonschema:"only the view for this client catalog"`
}
type ViewList struct {
	Items []View `json:"items"`
}

func validateLink(raw string) error {
	if u, e := url.Parse(raw); e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 2048 {
		return invalid("A link block needs an http or https url")
	}
	return nil
}

func validateView(d ViewData) error {
	if !oneOf(d.SubjectKind, subjectKinds...) {
		return invalid("subject_kind must be goal, task or event")
	}
	if _, e := id(d.SubjectID); e != nil {
		return invalid("subject_id must be a UUID")
	}
	if _, e := trackerCatalog(d.CatalogID); e != nil {
		return e
	}
	if len(d.Metrics) > 6 {
		return invalid("A view needs 0-6 metrics")
	}
	plain := make([]TrackerMetric, len(d.Metrics))
	byID := map[string]ViewMetric{}
	for i, m := range d.Metrics {
		plain[i], byID[m.ID] = m.TrackerMetric, m
		if m.Source != nil {
			if m.TaskID != "" {
				return invalid("A metric reads a provider series or a repeating task, not both")
			}
			if !trackerIdentifier.MatchString(m.Source.Provider) || !trackerIdentifier.MatchString(m.Source.Series) {
				return invalid("A metric source needs a lowercase provider and series identifier")
			}
			if g := m.Source.Granularity; g != "" && !kitBuckets[g] {
				return invalid("A source granularity is day, week or month")
			}
		}
		if m.TaskID != "" {
			if _, e := id(m.TaskID); e != nil {
				return invalid("task_id must be a UUID")
			}
		}
	}
	if _, e := validateMetrics(plain); e != nil {
		return e
	}
	return validatePages(d.Pages, byID)
}

func validatePages(pages []ViewPage, metrics map[string]ViewMetric) error {
	if len(pages) < 1 || len(pages) > 4 {
		return invalid("A view has 1-4 pages")
	}
	ids, nodes, total := map[string]bool{}, 0, 0
	unique := func(kind, id string) bool {
		if !trackerIdentifier.MatchString(id) || ids[kind+id] {
			return false
		}
		ids[kind+id] = true
		return true
	}
	for _, pg := range pages {
		if !unique("page", pg.ID) || len(pg.Title) > 24 || len(pages) > 1 && strings.TrimSpace(pg.Title) == "" {
			return invalid("Page ids must be unique identifiers; a page title is at most 24 characters and required when there are several pages")
		}
		if len(pg.Sections) < 1 || len(pg.Sections) > 6 {
			return invalid("A page has 1-6 sections")
		}
		for _, sec := range pg.Sections {
			if !unique("section", sec.ID) || strings.TrimSpace(sec.Title) == "" || len(sec.Title) > 60 || len(sec.Intro) > 240 {
				return invalid("Section ids must be unique identifiers, a section title is 1-60 characters and its intro at most 240")
			}
			if len(sec.Blocks) < 1 || len(sec.Blocks) > 8 {
				return invalid("A section has 1-8 blocks")
			}
			for _, b := range sec.Blocks {
				if total++; total > 16 {
					return invalid("A view has at most 16 blocks in all")
				}
				if !unique("block", b.ID) || len(b.Title) > 80 {
					return invalid("Block ids must be unique identifiers and a heading is at most 80 characters")
				}
				if e := validateBlock(b, metrics, &nodes); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

const viewCols = `id::text,subject_kind,subject_id::text,catalog_id,metrics,pages,version,created_at,updated_at`

func scanView(row scanner) (View, error) {
	var v View
	var metrics, pages []byte
	if e := row.Scan(&v.ID, &v.SubjectKind, &v.SubjectID, &v.CatalogID, &metrics, &pages, &v.Version, &v.CreatedAt, &v.UpdatedAt); e != nil {
		return v, e
	}
	v.Metrics, v.Pages = []ViewMetric{}, []ViewPage{}
	if e := json.Unmarshal(metrics, &v.Metrics); e != nil {
		return v, e
	}
	return v, json.Unmarshal(pages, &v.Pages)
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
	if e = u.subjectExists(ctx, in.SubjectKind, in.SubjectID); e != nil {
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
	pages, _ := json.Marshal(in.Pages)
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.views(id,subject_kind,subject_id,catalog_id,metrics,pages) VALUES($1,$2,$3,$4,$5,$6)`,
		key, in.SubjectKind, in.SubjectID, in.CatalogID, metrics, pages); e != nil {
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
	if !oneOf(in.SubjectKind, subjectKinds...) {
		return out, invalid("subject_kind must be goal, task or event")
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
	next.Metrics, next.Pages = in.Metrics, in.Pages
	if e = validateView(next); e != nil {
		return old, e
	}
	if e = u.viewKeepsRecordedMetrics(ctx, old, in.Metrics); e != nil {
		return old, e
	}
	metrics, _ := json.Marshal(in.Metrics)
	pages, _ := json.Marshal(in.Pages)
	if _, e = u.tx.Exec(ctx, `UPDATE life.views SET metrics=$2,pages=$3,version=version+1,updated_at=now() WHERE id=$1`, old.ID, metrics, pages); e != nil {
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
	rows, e := u.tx.Query(ctx, `SELECT DISTINCT metric_id,unit FROM life.measurements WHERE subject_kind=$1 AND subject_id=$2`, old.SubjectKind, old.SubjectID)
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
	SubjectKind string `json:"subject_kind" jsonschema:"goal, task or event"`
	SubjectID   string `json:"subject_id"`
	CatalogID   string `json:"catalog_id" jsonschema:"exact catalog supported by the requesting client"`
}
type ViewState struct {
	SubjectKind  string        `json:"subject_kind"`
	SubjectID    string        `json:"subject_id"`
	CatalogID    string        `json:"catalog_id"`
	View         *View         `json:"view" jsonschema:"the subject's view for this catalog, or null; never silently use another app's catalog"`
	Measurements []Measurement `json:"measurements" jsonschema:"the latest 120 recorded observations per metric, oldest first"`
}

// viewState is what a client opens a subject's view with: the view for the client's catalog and the observations recorded
// against its metrics.
func viewState(ctx context.Context, u *unit, in ViewStateInput) (ViewState, error) {
	out := ViewState{CatalogID: in.CatalogID, Measurements: []Measurement{}}
	if _, e := trackerCatalog(in.CatalogID); e != nil {
		return out, e
	}
	if !oneOf(in.SubjectKind, subjectKinds...) {
		return out, invalid("subject_kind must be goal, task or event")
	}
	subject, e := id(in.SubjectID)
	if e != nil {
		return out, invalid("subject_id must be a UUID")
	}
	if e = u.subjectExists(ctx, in.SubjectKind, subject); e != nil {
		return out, e
	}
	out.SubjectKind, out.SubjectID = in.SubjectKind, subject
	views, e := listViews(ctx, u, ViewsListInput{SubjectKind: in.SubjectKind, SubjectID: subject, CatalogID: in.CatalogID})
	if e != nil {
		return out, e
	}
	if len(views.Items) > 0 {
		out.View = &views.Items[0]
	}
	rows, e := u.tx.Query(ctx, `SELECT `+measurementCols+` FROM life.measurements WHERE id IN (
      SELECT id FROM (SELECT id,row_number() OVER(PARTITION BY metric_id ORDER BY measured_at DESC,id DESC) AS n FROM life.measurements WHERE subject_kind=$1 AND subject_id=$2 AND deleted_at IS NULL) recent WHERE n<=120)
      ORDER BY measured_at,id`, in.SubjectKind, subject)
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

// subjectKinds are what a view can be of and what observations can be recorded against.
var subjectKinds = []string{"goal", "task", "event"}

// subjectExists refuses a subject that is not there or is archived or deleted.
func (u *unit) subjectExists(ctx context.Context, kind, rawID string) error {
	key, e := id(rawID)
	if e != nil {
		return invalid("subject_id must be a UUID")
	}
	q := "SELECT EXISTS(SELECT 1 FROM life.items WHERE id=$1 AND kind=$2 AND deleted_at IS NULL)"
	if kind == "goal" {
		q = "SELECT EXISTS(SELECT 1 FROM life.goals WHERE id=$1 AND $2::text=$2 AND deleted_at IS NULL)"
	}
	var found bool
	if e = u.tx.QueryRow(ctx, q, key, kind).Scan(&found); e != nil {
		return e
	}
	if !found {
		return invalid("The subject is not a " + kind + " that exists")
	}
	return nil
}
