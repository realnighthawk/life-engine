package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// The feed and beliefs below back the clients' Today and You screens (Project Hub, Mission Control ADR 0012). They are
// read models over records the engine already holds: nothing here is a table. The feed is a placeholder in one respect:
// the engine does not know which agent raised an item or how much it matters, so agent_id is empty (the gateway fills
// it) and importance comes from the kind of record.

type FeedAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type FeedLink struct {
	Entity string `json:"entity" jsonschema:"commitment, decision, task or event"`
	ID     string `json:"id"`
}
type FeedItem struct {
	ID         string       `json:"id" jsonschema:"stable: kind:record id"`
	Type       string       `json:"type" jsonschema:"need, done, noticed or soon"`
	Importance int          `json:"importance" jsonschema:"1 to 3, from the kind of record until agents can set it"`
	AgentID    string       `json:"agent_id" jsonschema:"the agent that raised it, or an empty string when the engine does not know"`
	Title      string       `json:"title"`
	Detail     string       `json:"detail"`
	Deadline   *time.Time   `json:"deadline" jsonschema:"when it stops mattering, or null"`
	Since      time.Time    `json:"since" jsonschema:"when it was raised"`
	At         *time.Time   `json:"at" jsonschema:"when it happened or will happen, for the timeline, or null"`
	Actions    []FeedAction `json:"actions"`
	Link       *FeedLink    `json:"link,omitempty"`
}
type Feed struct {
	Items []FeedItem `json:"items" jsonschema:"what needs the person, then what happened and what is coming today"`
}

func feedGet(ctx context.Context, u *unit, _ struct{}) (Feed, error) {
	out := Feed{Items: []FeedItem{}}
	att, e := attentionGet(ctx, u, struct{}{})
	if e != nil {
		return out, e
	}
	for _, c := range att.Commitments {
		it := FeedItem{ID: "commitment:" + c.ID, Type: "need", Importance: 2, Title: c.Title, Detail: c.Notes, Deadline: c.DueAt, Since: c.CreatedAt,
			Actions: []FeedAction{}, Link: &FeedLink{Entity: "commitment", ID: c.ID}}
		out.Items = append(out.Items, it)
	}
	for _, d := range att.Decisions {
		out.Items = append(out.Items, FeedItem{ID: "decision:" + d.ID, Type: "need", Importance: 2, Title: d.Question, Detail: d.Subject, Deadline: d.ExpiresAt, Since: d.CreatedAt,
			Actions: []FeedAction{{ID: "open", Label: "Decide"}}, Link: &FeedLink{Entity: "decision", ID: d.ID}})
	}
	overdue := map[string]bool{}
	for _, t := range att.Tasks {
		overdue[t.ID] = true
		importance := 2
		if t.IsPriority {
			importance = 3
		}
		at := t.At
		out.Items = append(out.Items, FeedItem{ID: "task:" + t.ID, Type: "need", Importance: importance, Title: t.Title, Detail: t.Notes, Deadline: at, Since: t.CreatedAt,
			Actions: []FeedAction{{ID: "done", Label: "Done"}}, Link: &FeedLink{Entity: "task", ID: t.ID}})
	}
	day, e := dayView(ctx, u, DayInput{})
	if e != nil {
		return out, e
	}
	for _, en := range day.Entries {
		at := en.At
		switch {
		case en.Task != nil && overdue[en.Task.ID]:
			continue
		case en.Task != nil && en.State == "done":
			out.Items = append(out.Items, FeedItem{ID: "day:" + en.Task.ID + ":" + strconv.FormatInt(at.Unix(), 10), Type: "done", Importance: 1, Title: en.Task.Title, Detail: en.Task.Notes,
				Since: en.Task.CreatedAt, At: &at, Actions: []FeedAction{}, Link: &FeedLink{Entity: "task", ID: en.Task.ID}})
		case en.Task != nil:
			out.Items = append(out.Items, FeedItem{ID: "day:" + en.Task.ID + ":" + strconv.FormatInt(at.Unix(), 10), Type: "soon", Importance: 1, Title: en.Task.Title, Detail: en.Task.Notes,
				Since: en.Task.CreatedAt, At: &at, Actions: []FeedAction{}, Link: &FeedLink{Entity: "task", ID: en.Task.ID}})
		case en.Event != nil && en.State == "upcoming":
			out.Items = append(out.Items, FeedItem{ID: "day:" + en.Event.ID + ":" + strconv.FormatInt(at.Unix(), 10), Type: "soon", Importance: 1, Title: en.Event.Title, Detail: en.Event.Notes,
				Since: en.Event.CreatedAt, At: &at, Actions: []FeedAction{}, Link: &FeedLink{Entity: "event", ID: en.Event.ID}})
		}
	}
	return out, nil
}

// FeedActInput answers a feed item. Placeholder: acting on the underlying record already has its own operation
// (tasks_complete, decisions_resolve), and routing an answer to the agent that asked belongs to the gateway.
type FeedActInput struct {
	Meta
	ID     string `json:"id" jsonschema:"the feed item id"`
	Action string `json:"action" jsonschema:"the action id the item offered"`
}
type FeedActResult struct {
	Accepted bool `json:"accepted"`
}

func feedAct(_ context.Context, _ *unit, _ FeedActInput) (FeedActResult, error) {
	return FeedActResult{}, invalid("Feed actions are not implemented yet. Complete the task with tasks_complete or answer the decision with decisions_resolve.")
}

// A belief is something the system holds to be true about the person, with where it came from. Confirmed states are the
// world and persona lenses; active intentions are the goals lens.
type BeliefSource struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	At    string `json:"at"`
}
type Belief struct {
	ID         string         `json:"id" jsonschema:"state:id or intention:id"`
	Lens       string         `json:"lens" jsonschema:"world, persona or goals"`
	Topic      string         `json:"topic"`
	Summary    string         `json:"summary"`
	Detail     string         `json:"detail"`
	Confidence string         `json:"confidence" jsonschema:"low, fair or high"`
	Sources    []BeliefSource `json:"sources"`
	UsedBy     []string       `json:"used_by" jsonschema:"agent ids that rely on it; empty until the engine can say"`
	Version    int64          `json:"version" jsonschema:"for a later correction"`
}
type Beliefs struct {
	Beliefs []Belief `json:"beliefs"`
}

func valueText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func beliefsList(ctx context.Context, u *unit, _ struct{}) (Beliefs, error) {
	out := Beliefs{Beliefs: []Belief{}}
	states, e := listStates(ctx, u, ListStatesInput{ListInput: ListInput{Limit: 200}, Kind: "all"})
	if e != nil {
		return out, e
	}
	for _, s := range states.Items {
		if s.Status != "confirmed" {
			continue
		}
		lens := "persona"
		if s.Kind == "fact" {
			lens = "world"
		}
		confidence, who := "high", "You told us"
		switch s.Source {
		case "observation":
			confidence, who = "fair", "Noticed from your activity"
		case "agent":
			confidence, who = "low", "An agent recorded this"
		}
		summary := s.Topic + ": " + s.Predicate
		if v := valueText(s.Value); v != "" {
			summary += " " + v
		}
		detail := s.Notes
		if detail == "" {
			detail = summary
		}
		out.Beliefs = append(out.Beliefs, Belief{ID: "state:" + s.ID, Lens: lens, Topic: s.Topic, Summary: summary, Detail: detail, Confidence: confidence,
			Sources: []BeliefSource{{Kind: s.Source, Label: who, At: s.CreatedAt.Format(time.RFC3339)}}, UsedBy: []string{}, Version: s.Version})
	}
	intentions, e := listIntentions(ctx, u, ListIntentionsInput{ListInput: ListInput{Limit: 200}})
	if e != nil {
		return out, e
	}
	for _, in := range intentions.Items {
		detail := in.Notes
		if detail == "" {
			detail = in.Title
		}
		out.Beliefs = append(out.Beliefs, Belief{ID: "intention:" + in.ID, Lens: "goals", Topic: in.Kind, Summary: in.Title, Detail: detail, Confidence: "high",
			Sources: []BeliefSource{{Kind: "goals", Label: "Your goals", At: in.CreatedAt.Format(time.RFC3339)}}, UsedBy: []string{}, Version: in.Version})
	}
	return out, nil
}

// BeliefReviewInput confirms or corrects a state-backed belief. A goal is changed through the intention operations.
type BeliefReviewInput struct {
	Meta
	ID              string `json:"id" jsonschema:"state:id"`
	ExpectedVersion int64  `json:"expected_version"`
	Action          string `json:"action" jsonschema:"confirm or correct"`
	Detail          string `json:"detail,omitempty" jsonschema:"the corrected value, for correct"`
}

func beliefsReview(ctx context.Context, u *unit, in BeliefReviewInput) (State, error) {
	id, ok := strings.CutPrefix(in.ID, "state:")
	if !ok {
		return State{}, invalid("Only state-backed beliefs can be reviewed here; change a goal with the intention operations.")
	}
	if in.Action != "confirm" && in.Action != "correct" {
		return State{}, invalid("action must be confirm or correct")
	}
	old, e := u.getState(ctx, id, false)
	if e != nil {
		return State{}, e
	}
	value := old.Value
	if in.Action == "correct" {
		text := strings.TrimSpace(in.Detail)
		if text == "" {
			return State{}, invalid("detail is required to correct a belief")
		}
		value, _ = json.Marshal(text)
	}
	data := StateData{Kind: old.Kind, Topic: old.Topic, Predicate: old.Predicate, Value: value, Notes: old.Notes, Source: "person", Status: "confirmed", Subject: old.Subject, Origin: "person"}
	if old.ValidFrom != nil {
		s := old.ValidFrom.Format(time.RFC3339)
		data.ValidFrom = &s
	}
	if old.ValidTo != nil {
		s := old.ValidTo.Format(time.RFC3339)
		data.ValidTo = &s
	}
	return updateState(ctx, u, UpdateStateInput{EditInput: EditInput{Meta: in.Meta, ID: id, ExpectedVersion: in.ExpectedVersion}, StateData: data})
}
