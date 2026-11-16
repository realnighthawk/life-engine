package engine

import (
	"context"
	"encoding/json"
	"testing"
)

func TestValueText(t *testing.T) {
	for raw, want := range map[string]string{`"Lisbon"`: "Lisbon", `3`: "3", ``: "", `true`: "true"} {
		if got := valueText(json.RawMessage(raw)); got != want {
			t.Errorf("valueText(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestBeliefsReviewRejectsWhatItCannotChange(t *testing.T) {
	ctx := context.Background()
	if _, err := beliefsReview(ctx, nil, BeliefReviewInput{ID: "intention:abc", Action: "confirm"}); err == nil {
		t.Error("a goal belief must not be reviewed here")
	}
	if _, err := beliefsReview(ctx, nil, BeliefReviewInput{ID: "state:abc", Action: "forget"}); err == nil {
		t.Error("an unknown action must be rejected")
	}
}

func TestFeedActDeclines(t *testing.T) {
	if _, err := feedAct(context.Background(), nil, FeedActInput{ID: "task:1", Action: "done"}); err == nil {
		t.Error("the placeholder must decline")
	}
}
