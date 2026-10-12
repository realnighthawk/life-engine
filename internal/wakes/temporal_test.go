package wakes

import (
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// TestScheduleShape pins what tenant-worker/tenant_worker/tools_wake.py arm_wake builds for a one-shot time wake: same id
// scheme, same workflow, same input keys, one action, skip overlap, and a calendar spec for the exact second (not an
// interval; see temporal.go). If arm_wake changes, this changes with it. TestLiveTemporal proves it fires when set.
func TestScheduleShape(t *testing.T) {
	w := &Temporal{cfg: Config{Namespace: "agents", TaskQueue: "agent-loop", TenantSlug: "acme", SessionKey: "agent:main:ios:user:u1"}}
	at := time.Date(2026, 10, 12, 17, 0, 0, 0, time.UTC)
	o := w.options(engine.Wake{Name: "reminder-1", Kind: "reminder", Subject: "reminder:1", Objective: "do it", Why: "because", At: at})

	id := "wake:agent:main:ios:user:u1:reminder-1"
	if o.ID != id || o.RemainingActions != 1 || o.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Fatalf("schedule: %+v", o)
	}
	if len(o.Spec.Calendars) != 1 || o.Spec.TimeZoneName != "UTC" {
		t.Fatalf("spec: %+v", o.Spec)
	}
	c := o.Spec.Calendars[0]
	if c.Year[0].Start != 2026 || c.Month[0].Start != 10 || c.DayOfMonth[0].Start != 12 || c.Hour[0].Start != 17 || c.Minute[0].Start != 0 || c.Second[0].Start != 0 {
		t.Fatalf("calendar: %+v", c)
	}
	act := o.Action.(*client.ScheduleWorkflowAction)
	if act.Workflow != "WakeWorkflow" || act.TaskQueue != "agent-loop" || act.ID != id {
		t.Fatalf("action: %+v", act)
	}
	in := act.Args[0].(map[string]any)
	want := map[string]any{"wake_id": id, "session_key": "agent:main:ios:user:u1", "tenant_slug": "acme", "objective": "do it", "why": "because"}
	if len(in) != len(want) {
		t.Fatalf("input keys: %v", in)
	}
	for k, v := range want {
		if in[k] != v {
			t.Fatalf("input %s = %v, want %v", k, in[k], v)
		}
	}
}
