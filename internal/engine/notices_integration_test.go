package engine_test

import (
	"testing"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestNotifyIsHandedOverOnceAndOnlyOnce(t *testing.T) {
	s := integrationService(t)
	id := "5b1e5d3c-9f0a-4c2e-8f3a-0a1b2c3d4e5f"
	in := map[string]any{"idempotency_key": key(), "id": id, "title": "Weigh-in", "body": "It is Sunday", "priority": "high", "link": map[string]any{"entity": "routine", "id": "r1"}}
	execute[engine.NotifyResult](t, s, "notify", in)
	in["idempotency_key"] = key() // same notice id, new request: still one notice
	execute[engine.NotifyResult](t, s, "notify", in)

	pending := execute[engine.EventsResult](t, s, "events_pending", map[string]any{})
	var found []engine.Event
	for _, e := range pending.Items {
		if e.EventKey == "life:notice" {
			found = append(found, e)
		}
	}
	if len(found) != 1 || found[0].EventID != "notice:"+id || found[0].Notice.Title != "Weigh-in" || found[0].Notice.Priority != "high" || found[0].Notice.Link == nil || found[0].Notice.Link.Entity != "routine" {
		t.Fatalf("one notice with its link: %+v", found)
	}
	execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{found[0].EventID}})
	for _, e := range execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items {
		if e.EventKey == "life:notice" {
			t.Fatalf("an acknowledged notice is not handed over again: %+v", e)
		}
	}
}
