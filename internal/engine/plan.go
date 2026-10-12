package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type PlanItem struct {
	ID         string     `json:"id"`
	Day        string     `json:"day" jsonschema:"local date YYYY-MM-DD"`
	Title      string     `json:"title"`
	Kind       string     `json:"kind" jsonschema:"task, event, prep or rest"`
	Minutes    int        `json:"minutes"`
	StartAt    *time.Time `json:"start_at"`
	IsPriority bool       `json:"is_priority"`
	GoalID     string     `json:"goal_id,omitempty"`
	StepID     string     `json:"step_id,omitempty"`
	DoneAt     *time.Time `json:"done_at"`
	Version    int64      `json:"version"`
	DeletedAt  *time.Time `json:"deleted_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type PlanItemData struct {
	Day        string  `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
	Title      string  `json:"title" jsonschema:"1-200 characters"`
	Kind       string  `json:"kind,omitempty" jsonschema:"task (default), event, prep or rest"`
	Minutes    int     `json:"minutes,omitempty" jsonschema:"1-720, default 25"`
	StartAt    *string `json:"start_at,omitempty" jsonschema:"RFC 3339 start time, optional"`
	IsPriority bool    `json:"is_priority,omitempty" jsonschema:"one of the few things that matter most today"`
	GoalID     string  `json:"goal_id,omitempty"`
	StepID     string  `json:"step_id,omitempty" jsonschema:"completing the item also completes this goal step"`
}
type CreatePlanItemInput struct {
	Meta
	ID string `json:"id,omitempty"`
	PlanItemData
}
type UpdatePlanItemInput struct {
	EditInput
	PlanItemData
}
type MovePlanItemInput struct {
	EditInput
	Day string `json:"day" jsonschema:"local date YYYY-MM-DD to move it to; clears its start time"`
}
type PlanDayInput struct {
	Day string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
}
type PlanSummary struct {
	Planned       int `json:"planned" jsonschema:"non-event items on the day"`
	Done          int `json:"done"`
	MinutesOpen   int `json:"minutes_open"`
	MinutesDone   int `json:"minutes_done"`
	MinutesEvents int `json:"minutes_events"`
}
type PlanDay struct {
	Day       string         `json:"day"`
	Items     []PlanItem     `json:"items"`
	Proposals []PlanProposal `json:"proposals" jsonschema:"pending revisions for this day"`
	Summary   PlanSummary    `json:"summary"`
}

const planCols = `id::text,day::text,title,kind,minutes,start_at,is_priority,coalesce(goal_id::text,''),coalesce(step_id::text,''),done_at,version,deleted_at,created_at,updated_at`

func scanPlanItem(row scanner) (PlanItem, error) {
	var p PlanItem
	err := row.Scan(&p.ID, &p.Day, &p.Title, &p.Kind, &p.Minutes, &p.StartAt, &p.IsPriority, &p.GoalID, &p.StepID, &p.DoneAt, &p.Version, &p.DeletedAt, &p.CreatedAt, &p.UpdatedAt)
	utcPtr(p.StartAt)
	utcPtr(p.DoneAt)
	utcPtr(p.DeletedAt)
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

func (u *unit) getPlanItem(ctx context.Context, key string, forUpdate bool) (PlanItem, error) {
	k, e := id(key)
	if e != nil {
		return PlanItem{}, e
	}
	q := "SELECT " + planCols + " FROM life.plan_items WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanPlanItem(u.tx.QueryRow(ctx, q, k))
}

func (u *unit) planItemsOn(ctx context.Context, d string) ([]PlanItem, error) {
	rows, err := u.tx.Query(ctx, "SELECT "+planCols+` FROM life.plan_items WHERE day=$1 AND deleted_at IS NULL
		ORDER BY start_at NULLS LAST, is_priority DESC, created_at, id`, d)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PlanItem{}
	for rows.Next() {
		p, err := scanPlanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// refExists checks an optional goal/step reference before use, so a bad id is invalid_input rather than a constraint error.
func (u *unit) refExists(ctx context.Context, table, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	k, e := id(key)
	if e != nil {
		return "", e
	}
	var found bool
	if e = u.tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM life."+table+" WHERE id=$1)", k).Scan(&found); e != nil {
		return "", e
	}
	if !found {
		return "", invalid(strings.TrimSuffix(table, "s") + " does not exist")
	}
	return k, nil
}

type planValues struct {
	day, title, kind, goal, step string
	minutes                      int
	start                        *time.Time
	priority                     bool
}

func (u *unit) planValues(ctx context.Context, d PlanItemData) (planValues, error) {
	v := planValues{title: strings.TrimSpace(d.Title), kind: d.Kind, minutes: d.Minutes, priority: d.IsPriority}
	if v.title == "" || len(v.title) > 200 {
		return v, invalid("title must be 1-200 characters")
	}
	if v.kind == "" {
		v.kind = "task"
	}
	if !oneOf(v.kind, "task", "event", "prep", "rest") {
		return v, invalid("kind must be task, event, prep or rest")
	}
	if v.minutes == 0 {
		v.minutes = 25
	}
	if v.minutes < 1 || v.minutes > 720 {
		return v, invalid("minutes must be 1-720")
	}
	var e error
	if d.Day == "" {
		today, _, _, e := u.today(ctx)
		if e != nil {
			return v, e
		}
		v.day = today
	} else if v.day, e = day("day", d.Day); e != nil {
		return v, e
	}
	if d.StartAt != nil {
		t, e := instant("start_at", *d.StartAt)
		if e != nil {
			return v, e
		}
		v.start = &t
	}
	if v.goal, e = u.refExists(ctx, "goals", d.GoalID); e != nil {
		return v, e
	}
	if v.step, e = u.refExists(ctx, "steps", d.StepID); e != nil {
		return v, e
	}
	return v, nil
}

func createPlanItem(ctx context.Context, u *unit, in CreatePlanItemInput) (PlanItem, error) {
	key, e := createID(in.ID)
	if e != nil {
		return PlanItem{}, e
	}
	v, e := u.planValues(ctx, in.PlanItemData)
	if e != nil {
		return PlanItem{}, e
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.plan_items(id,day,title,kind,minutes,start_at,is_priority,goal_id,step_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid)`,
		key, v.day, v.title, v.kind, v.minutes, v.start, v.priority, v.goal, v.step); e != nil {
		return PlanItem{}, e
	}
	p, e := u.getPlanItem(ctx, key, false)
	if e != nil {
		return p, e
	}
	return p, u.audit(ctx, "plan_item", key, nil, p)
}

func (u *unit) planItemChanged(ctx context.Context, old PlanItem) (PlanItem, error) {
	p, e := u.getPlanItem(ctx, old.ID, false)
	if e != nil {
		return p, e
	}
	return p, u.audit(ctx, "plan_item", p.ID, old, p)
}

func (u *unit) editablePlanItem(ctx context.Context, in EditInput) (PlanItem, error) {
	old, e := u.getPlanItem(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.DeletedAt != nil {
		return old, conflict("Restore the item before changing it")
	}
	return old, nil
}

func updatePlanItem(ctx context.Context, u *unit, in UpdatePlanItemInput) (PlanItem, error) {
	old, e := u.editablePlanItem(ctx, in.EditInput)
	if e != nil {
		return old, e
	}
	v, e := u.planValues(ctx, in.PlanItemData)
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET day=$2,title=$3,kind=$4,minutes=$5,start_at=$6,is_priority=$7,goal_id=NULLIF($8,'')::uuid,step_id=NULLIF($9,'')::uuid,version=version+1,updated_at=now() WHERE id=$1`,
		old.ID, v.day, v.title, v.kind, v.minutes, v.start, v.priority, v.goal, v.step); e != nil {
		return old, e
	}
	return u.planItemChanged(ctx, old)
}

// completePlanItem marks an item done or reopens it; a linked goal step follows it, in the same transaction.
func completePlanItem(ctx context.Context, u *unit, in CompleteInput) (PlanItem, error) {
	old, e := u.editablePlanItem(ctx, in.EditInput)
	if e != nil {
		return old, e
	}
	if (old.DoneAt != nil) != in.Done {
		val := "NULL"
		if in.Done {
			val = "now()"
		}
		if _, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET done_at=`+val+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
			return old, e
		}
	}
	if old.StepID != "" {
		step, e := u.getStep(ctx, old.StepID, true)
		if e == nil {
			if _, e = u.setStepDone(ctx, step, in.Done); e != nil {
				return old, e
			}
		}
	}
	return u.planItemChanged(ctx, old)
}

func movePlanItem(ctx context.Context, u *unit, in MovePlanItemInput) (PlanItem, error) {
	old, e := u.editablePlanItem(ctx, in.EditInput)
	if e != nil {
		return old, e
	}
	d, e := day("day", in.Day)
	if e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET day=$2,start_at=NULL,version=version+1,updated_at=now() WHERE id=$1`, old.ID, d); e != nil {
		return old, e
	}
	return u.planItemChanged(ctx, old)
}

func lifecyclePlanItem(ctx context.Context, u *unit, in EditInput, restore bool) (PlanItem, error) {
	old, e := u.getPlanItem(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if restore == (old.DeletedAt == nil) {
		if restore {
			return old, conflict("Item is not deleted")
		}
		return old, conflict("Item is already deleted")
	}
	set := "deleted_at=now()"
	if restore {
		set = "deleted_at=NULL"
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET `+set+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.planItemChanged(ctx, old)
}

func summarize(items []PlanItem) PlanSummary {
	var s PlanSummary
	for _, it := range items {
		switch {
		case it.Kind == "event":
			s.MinutesEvents += it.Minutes
		case it.DoneAt != nil:
			s.Planned++
			s.Done++
			s.MinutesDone += it.Minutes
		default:
			s.Planned++
			s.MinutesOpen += it.Minutes
		}
	}
	return s
}

func (u *unit) planDay(ctx context.Context, d string) (PlanDay, error) {
	items, e := u.planItemsOn(ctx, d)
	if e != nil {
		return PlanDay{}, e
	}
	props, e := u.pendingProposals(ctx, d)
	if e != nil {
		return PlanDay{}, e
	}
	return PlanDay{Day: d, Items: items, Proposals: props, Summary: summarize(items)}, nil
}

func (u *unit) dayOrToday(ctx context.Context, d string) (string, error) {
	if d != "" {
		return day("day", d)
	}
	today, _, _, e := u.today(ctx)
	return today, e
}

func getPlan(ctx context.Context, u *unit, in PlanDayInput) (PlanDay, error) {
	d, e := u.dayOrToday(ctx, in.Day)
	if e != nil {
		return PlanDay{}, e
	}
	return u.planDay(ctx, d)
}

// ---- deterministic: replan preview and next action

type ReplanInput struct {
	Day         string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
	MinutesLeft int    `json:"minutes_left" jsonschema:"how many minutes of work the person can still do that day, 0-1440"`
}

func previewReplan(ctx context.Context, u *unit, in ReplanInput) (Replan, error) {
	if in.MinutesLeft < 0 || in.MinutesLeft > 1440 {
		return Replan{}, invalid("minutes_left must be 0-1440")
	}
	d, e := u.dayOrToday(ctx, in.Day)
	if e != nil {
		return Replan{}, e
	}
	items, e := u.planItemsOn(ctx, d)
	if e != nil {
		return Replan{}, e
	}
	return replan(items, in.MinutesLeft), nil
}

type NextActionInput struct {
	Minutes int    `json:"minutes" jsonschema:"how much time the person has, 1-480"`
	Day     string `json:"day,omitempty" jsonschema:"local date YYYY-MM-DD; default today"`
}
type NextActionResult struct {
	Suggestion *Suggestion `json:"suggestion" jsonschema:"null when nothing planned fits the time"`
}

func nextAction(ctx context.Context, u *unit, in NextActionInput) (NextActionResult, error) {
	if in.Minutes < 1 || in.Minutes > 480 {
		return NextActionResult{}, invalid("minutes must be 1-480")
	}
	d, e := u.dayOrToday(ctx, in.Day)
	if e != nil {
		return NextActionResult{}, e
	}
	items, e := u.planItemsOn(ctx, d)
	if e != nil {
		return NextActionResult{}, e
	}
	goals, e := u.goals(ctx, "WHERE archived_at IS NULL ORDER BY created_at, id")
	if e != nil {
		return NextActionResult{}, e
	}
	return NextActionResult{Suggestion: suggestNext(in.Minutes, items, goals)}, nil
}

// ---- proposals: a revised plan offered by the agent and accepted by the person

type ProposalChange struct {
	Action     string `json:"action" jsonschema:"add, move or drop"`
	ItemID     string `json:"item_id,omitempty" jsonschema:"required for move and drop: the plan item affected"`
	Title      string `json:"title,omitempty" jsonschema:"required for add"`
	Minutes    int    `json:"minutes,omitempty"`
	Kind       string `json:"kind,omitempty"`
	IsPriority bool   `json:"is_priority,omitempty"`
	Day        string `json:"day,omitempty" jsonschema:"for move: the day to move it to (required); for add: the day to add it on (default the proposal's day)"`
}
type PlanProposal struct {
	ID        string           `json:"id"`
	Day       string           `json:"day"`
	Reason    string           `json:"reason"`
	Changes   []ProposalChange `json:"changes"`
	State     string           `json:"state" jsonschema:"pending, applied or discarded"`
	Version   int64            `json:"version"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}
type ProposeInput struct {
	Meta
	ID      string           `json:"id,omitempty"`
	Day     string           `json:"day" jsonschema:"local date YYYY-MM-DD the revision is about"`
	Reason  string           `json:"reason" jsonschema:"why, in the person's terms, for example 'Your afternoon meeting ran long'"`
	Changes []ProposalChange `json:"changes" jsonschema:"1-50 changes"`
}
type ListProposalsInput struct {
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	State  string `json:"state,omitempty" jsonschema:"pending (default), applied, discarded or all"`
}

const proposalCols = `id::text,day::text,reason,changes,state,version,created_at,updated_at`

func scanProposal(row scanner) (PlanProposal, error) {
	var p PlanProposal
	var changes []byte
	if err := row.Scan(&p.ID, &p.Day, &p.Reason, &changes, &p.State, &p.Version, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, json.Unmarshal(changes, &p.Changes)
}

func (u *unit) getProposal(ctx context.Context, key string, forUpdate bool) (PlanProposal, error) {
	k, e := id(key)
	if e != nil {
		return PlanProposal{}, e
	}
	q := "SELECT " + proposalCols + " FROM life.plan_proposals WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanProposal(u.tx.QueryRow(ctx, q, k))
}

func (u *unit) pendingProposals(ctx context.Context, d string) ([]PlanProposal, error) {
	rows, err := u.tx.Query(ctx, "SELECT "+proposalCols+" FROM life.plan_proposals WHERE day=$1 AND state='pending' ORDER BY created_at, id", d)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlanProposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (u *unit) checkChanges(ctx context.Context, d string, changes []ProposalChange) ([]ProposalChange, error) {
	if len(changes) < 1 || len(changes) > 50 {
		return nil, invalid("changes must contain 1-50 entries")
	}
	out := make([]ProposalChange, len(changes))
	for i, c := range changes {
		switch c.Action {
		case "add":
			if strings.TrimSpace(c.Title) == "" || len(c.Title) > 200 {
				return nil, invalid("an added change needs a title of 1-200 characters")
			}
			if c.Kind != "" && !oneOf(c.Kind, "task", "event", "prep", "rest") {
				return nil, invalid("kind must be task, event, prep or rest")
			}
			if c.Minutes < 0 || c.Minutes > 720 {
				return nil, invalid("minutes must be 1-720")
			}
			if c.Day != "" {
				if _, e := day("day", c.Day); e != nil {
					return nil, e
				}
			}
			c.Title = strings.TrimSpace(c.Title)
		case "move", "drop":
			key, e := id(c.ItemID)
			if e != nil {
				return nil, invalid(c.Action + " needs the item_id of a plan item")
			}
			item, e := u.getPlanItem(ctx, key, false)
			if e != nil || item.DeletedAt != nil {
				return nil, invalid(c.Action + " refers to a plan item that does not exist")
			}
			c.ItemID = key
			if c.Action == "move" {
				if _, e := day("day", c.Day); e != nil {
					return nil, invalid("move needs a day as YYYY-MM-DD")
				}
			}
		default:
			return nil, invalid("action must be add, move or drop")
		}
		out[i] = c
	}
	return out, nil
}

func createProposal(ctx context.Context, u *unit, in ProposeInput) (PlanProposal, error) {
	key, e := createID(in.ID)
	if e != nil {
		return PlanProposal{}, e
	}
	d, e := day("day", in.Day)
	if e != nil {
		return PlanProposal{}, e
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len(reason) > 1000 {
		return PlanProposal{}, invalid("reason must be 1-1000 characters")
	}
	changes, e := u.checkChanges(ctx, d, in.Changes)
	if e != nil {
		return PlanProposal{}, e
	}
	raw, _ := json.Marshal(changes)
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.plan_proposals(id,day,reason,changes) VALUES($1,$2,$3,$4)`, key, d, reason, raw); e != nil {
		return PlanProposal{}, e
	}
	p, e := u.getProposal(ctx, key, false)
	if e != nil {
		return p, e
	}
	return p, u.audit(ctx, "plan_proposal", key, nil, p)
}

func (u *unit) proposalChanged(ctx context.Context, old PlanProposal) (PlanProposal, error) {
	p, e := u.getProposal(ctx, old.ID, false)
	if e != nil {
		return p, e
	}
	return p, u.audit(ctx, "plan_proposal", p.ID, old, p)
}

func listProposals(ctx context.Context, u *unit, in ListProposalsInput) (Page[PlanProposal], error) {
	result := Page[PlanProposal]{Items: []PlanProposal{}}
	limit, offset, _, e := paging(ListInput{Limit: in.Limit, Offset: in.Offset})
	if e != nil {
		return result, e
	}
	state := in.State
	if state == "" {
		state = "pending"
	}
	if !oneOf(state, "pending", "applied", "discarded", "all") {
		return result, invalid("state must be pending, applied, discarded or all")
	}
	filter, args := "true", []any{}
	if state != "all" {
		filter, args = "state=$1", []any{state}
	}
	if e = u.tx.QueryRow(ctx, "SELECT count(*) FROM life.plan_proposals WHERE "+filter, args...).Scan(&result.Total); e != nil {
		return result, e
	}
	args = append(args, limit, offset)
	n := len(args)
	rows, e := u.tx.Query(ctx, "SELECT "+proposalCols+" FROM life.plan_proposals WHERE "+filter+" ORDER BY created_at DESC, id LIMIT $"+itoa(n-1)+" OFFSET $"+itoa(n), args...)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		p, e := scanProposal(rows)
		if e != nil {
			return result, e
		}
		result.Items = append(result.Items, p)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}

func discardProposal(ctx context.Context, u *unit, in EditInput) (PlanProposal, error) {
	old, e := u.getProposal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.State != "pending" {
		return old, conflict("Proposal is already " + old.State)
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.plan_proposals SET state='discarded',version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.proposalChanged(ctx, old)
}

// applyProposal carries out every change in one transaction; if the plan moved on since it was offered, nothing is applied.
func applyProposal(ctx context.Context, u *unit, in EditInput) (PlanProposal, error) {
	old, e := u.getProposal(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if old.State != "pending" {
		return old, conflict("Proposal is already " + old.State)
	}
	for _, c := range old.Changes {
		switch c.Action {
		case "add":
			d := c.Day
			if d == "" {
				d = old.Day
			}
			kind := c.Kind
			if kind == "" {
				kind = "task"
			}
			minutes := c.Minutes
			if minutes == 0 {
				minutes = 25
			}
			var nid string
			if e = u.tx.QueryRow(ctx, `INSERT INTO life.plan_items(day,title,kind,minutes,is_priority) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, d, c.Title, kind, minutes, c.IsPriority).Scan(&nid); e != nil {
				return old, e
			}
			item, e := u.getPlanItem(ctx, nid, false)
			if e != nil {
				return old, e
			}
			if e = u.audit(ctx, "plan_item", nid, nil, item); e != nil {
				return old, e
			}
		case "move", "drop":
			item, e := u.getPlanItem(ctx, c.ItemID, true)
			if e != nil || item.DeletedAt != nil {
				return old, conflict("The plan changed since this proposal was made; ask for a new one")
			}
			if c.Action == "move" {
				_, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET day=$2,start_at=NULL,version=version+1,updated_at=now() WHERE id=$1`, item.ID, c.Day)
			} else {
				_, e = u.tx.Exec(ctx, `UPDATE life.plan_items SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, item.ID)
			}
			if e != nil {
				return old, e
			}
			if _, e = u.planItemChanged(ctx, item); e != nil {
				return old, e
			}
		}
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.plan_proposals SET state='applied',version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return u.proposalChanged(ctx, old)
}
