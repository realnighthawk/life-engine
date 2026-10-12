package engine

import (
	"context"
	"strings"
	"time"
)

type Commitment struct {
	ID        string     `json:"id"`
	WeekStart string     `json:"week_start" jsonschema:"the local Monday of the week, YYYY-MM-DD"`
	Title     string     `json:"title"`
	GoalID    string     `json:"goal_id,omitempty"`
	StepID    string     `json:"step_id,omitempty"`
	DoneAt    *time.Time `json:"done_at"`
	Version   int64      `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
type AddCommitmentInput struct {
	Meta
	ID        string `json:"id,omitempty"`
	WeekStart string `json:"week_start,omitempty" jsonschema:"the local Monday, YYYY-MM-DD; default this week"`
	Title     string `json:"title,omitempty" jsonschema:"what the person commits to; defaults to the step's or goal's title"`
	GoalID    string `json:"goal_id,omitempty"`
	StepID    string `json:"step_id,omitempty" jsonschema:"completing the commitment also completes this step"`
}
type ListCommitmentsInput struct {
	WeekStart string `json:"week_start,omitempty" jsonschema:"the local Monday, YYYY-MM-DD; default this week"`
}
type CommitmentsWeek struct {
	WeekStart string       `json:"week_start"`
	Items     []Commitment `json:"items"`
	Done      int          `json:"done"`
	Total     int          `json:"total"`
}

const commitmentCols = `id::text,week_start::text,title,coalesce(goal_id::text,''),coalesce(step_id::text,''),done_at,version,created_at,updated_at`

func scanCommitment(row scanner) (Commitment, error) {
	var c Commitment
	err := row.Scan(&c.ID, &c.WeekStart, &c.Title, &c.GoalID, &c.StepID, &c.DoneAt, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	utcPtr(c.DoneAt)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, err
}

// mondayOf returns the Monday on or before a local date.
func mondayOf(d string) string {
	n := toDayNumber(d)
	return fromDayNumber(n - dayNumber((weekdayOf(n)+6)%7))
}

func (u *unit) weekStart(ctx context.Context, w string) (string, error) {
	if w == "" {
		today, _, _, e := u.today(ctx)
		return mondayOf(today), e
	}
	d, e := day("week_start", w)
	if e != nil {
		return "", e
	}
	if mondayOf(d) != d {
		return "", invalid("week_start must be a Monday")
	}
	return d, nil
}

func (u *unit) getCommitment(ctx context.Context, key string, forUpdate bool) (Commitment, error) {
	k, e := id(key)
	if e != nil {
		return Commitment{}, e
	}
	q := "SELECT " + commitmentCols + " FROM life.commitments WHERE id=$1"
	if forUpdate {
		q += " FOR UPDATE"
	}
	return scanCommitment(u.tx.QueryRow(ctx, q, k))
}

func addCommitment(ctx context.Context, u *unit, in AddCommitmentInput) (Commitment, error) {
	key, e := createID(in.ID)
	if e != nil {
		return Commitment{}, e
	}
	week, e := u.weekStart(ctx, in.WeekStart)
	if e != nil {
		return Commitment{}, e
	}
	title, goalID, stepID := strings.TrimSpace(in.Title), "", ""
	if in.StepID != "" {
		st, e := u.getStep(ctx, in.StepID, false)
		if e != nil {
			return Commitment{}, invalid("step does not exist")
		}
		stepID, goalID = st.ID, st.GoalID
		if title == "" {
			title = st.Title
		}
	}
	if in.GoalID != "" {
		g, e := u.getGoal(ctx, in.GoalID, false)
		if e != nil {
			return Commitment{}, invalid("goal does not exist")
		}
		if goalID != "" && goalID != g.ID {
			return Commitment{}, invalid("step does not belong to that goal")
		}
		goalID = g.ID
		if title == "" {
			title = g.Title
		}
	}
	if title == "" || len(title) > 200 {
		return Commitment{}, invalid("title must be 1-200 characters (or give a goal or step)")
	}
	if _, e = u.tx.Exec(ctx, `INSERT INTO life.commitments(id,week_start,title,goal_id,step_id) VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid)`, key, week, title, goalID, stepID); e != nil {
		return Commitment{}, e
	}
	c, e := u.getCommitment(ctx, key, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "commitment", key, nil, c)
}

func listCommitments(ctx context.Context, u *unit, in ListCommitmentsInput) (CommitmentsWeek, error) {
	week, e := u.weekStart(ctx, in.WeekStart)
	out := CommitmentsWeek{WeekStart: week, Items: []Commitment{}}
	if e != nil {
		return out, e
	}
	rows, e := u.tx.Query(ctx, "SELECT "+commitmentCols+" FROM life.commitments WHERE week_start=$1 ORDER BY created_at, id", week)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		c, e := scanCommitment(rows)
		if e != nil {
			return out, e
		}
		out.Total++
		if c.DoneAt != nil {
			out.Done++
		}
		out.Items = append(out.Items, c)
	}
	return out, rows.Err()
}

func completeCommitment(ctx context.Context, u *unit, in CompleteInput) (Commitment, error) {
	old, e := u.getCommitment(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if (old.DoneAt != nil) != in.Done {
		val := "NULL"
		if in.Done {
			val = "now()"
		}
		if _, e = u.tx.Exec(ctx, `UPDATE life.commitments SET done_at=`+val+`,version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
			return old, e
		}
	}
	if old.StepID != "" {
		if step, e := u.getStep(ctx, old.StepID, true); e == nil {
			if _, e = u.setStepDone(ctx, step, in.Done); e != nil {
				return old, e
			}
		}
	}
	c, e := u.getCommitment(ctx, old.ID, false)
	if e != nil {
		return c, e
	}
	return c, u.audit(ctx, "commitment", c.ID, old, c)
}

func deleteCommitment(ctx context.Context, u *unit, in EditInput) (Commitment, error) {
	old, e := u.getCommitment(ctx, in.ID, true)
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `DELETE FROM life.commitments WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	return old, u.audit(ctx, "commitment", old.ID, old, nil)
}
