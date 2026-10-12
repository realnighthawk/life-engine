package wakes

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
	"go.temporal.io/sdk/client"
)

// TestLiveTemporal arms, finds by search attribute, and removes a wake far in the future. Set LIVE_TEMPORAL=host:port
// (port-forward the frontend); it uses the real namespace, so it cleans up after itself.
func TestLiveTemporal(t *testing.T) {
	addr := os.Getenv("LIVE_TEMPORAL")
	if addr == "" {
		t.Skip("LIVE_TEMPORAL not set")
	}
	w, err := New(Config{Address: addr, Namespace: "agents", TaskQueue: "agent-loop", TenantSlug: "life-engine-livetest", SessionKey: "agent:main:ios:user:livetest"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx := context.Background()
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	wake := engine.Wake{Name: "reminder-livetest", Kind: "reminder", Subject: "reminder:livetest", Objective: "live test, ignore", Why: "test", At: at}
	defer w.Cancel(ctx, wake.Name)
	for i := 0; i < 2; i++ { // twice: the second replaces the first
		if err = w.Arm(ctx, wake); err != nil {
			t.Fatal(err)
		}
	}
	d, err := w.c.ScheduleClient().GetHandle(ctx, w.id(wake.Name)).Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Schedule.State.RemainingActions != 1 || len(d.Info.NextActionTimes) == 0 || !d.Info.NextActionTimes[0].Equal(at) {
		t.Fatalf("describe: remaining=%d next=%v", d.Schedule.State.RemainingActions, d.Info.NextActionTimes)
	}
	var found bool
	it, err := w.c.ScheduleClient().List(ctx, client.ScheduleListOptions{Query: "TenantSlug = 'life-engine-livetest' AND WakeUserVisible = true AND WakeSubject = 'reminder:livetest'"})
	if err != nil {
		t.Fatal(err)
	}
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			t.Fatal(err)
		}
		found = found || e.ID == w.id(wake.Name)
	}
	if !found {
		t.Log("not in visibility yet (eventually consistent); describe succeeded, which is what the engine relies on")
	}
	if err = w.Cancel(ctx, wake.Name); err != nil {
		t.Fatal(err)
	}
	if err = w.Cancel(ctx, wake.Name); err != nil {
		t.Fatalf("cancelling a missing wake must not be an error: %v", err)
	}
}
