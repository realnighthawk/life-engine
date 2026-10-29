package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestARecordIsFoundByUniquePrefixAndAMissNamesTheClosest(t *testing.T) {
	s := integrationService(t)
	task := execute[engine.Task](t, s, "tasks_create", map[string]any{"idempotency_key": key(), "title": "Call Sam"})

	got := execute[engine.Task](t, s, "tasks_get", map[string]any{"id": task.ID[:8]})
	if got.ID != task.ID {
		t.Fatalf("prefix resolved to %s, want %s", got.ID, task.ID)
	}

	mangled := task.ID[:20] + "0" + task.ID[21:] // one altered character
	if mangled == task.ID {
		mangled = task.ID[:20] + "1" + task.ID[21:]
	}
	raw, _ := json.Marshal(map[string]any{"id": mangled})
	_, e := s.Execute(context.Background(), "tasks_get", raw)
	pub := engine.PublicError(e)
	if pub.Code != "not_found" || !strings.Contains(pub.Message, task.ID) || !strings.Contains(pub.Message, "Call Sam") {
		t.Fatalf("a miss should name the closest record: %+v", pub)
	}

	errorCode(t, s, "tasks_get", map[string]any{"id": "ffffffff"}, "not_found") // no record has this prefix
	errorCode(t, s, "tasks_get", map[string]any{"id": "nope"}, "invalid_input")
}
