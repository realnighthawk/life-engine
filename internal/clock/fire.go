package clock

import (
	"context"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// The Schedule sets this on every workflow it starts: the instant the firing was scheduled for, which is what names the
// occurrence (a catch-up firing starts later than it was due).
var saScheduledStart = temporal.NewSearchAttributeKeyTime("TemporalScheduledStartTime")

// FireScheduled is what the Fire activity is given.
type FireScheduled struct {
	ItemID    string    `json:"item_id"`
	Scheduled time.Time `json:"scheduled"`
}

// FireWorkflow is one alert coming due. It tells the engine (which records a notice for a person's item, or says which
// conversation to wake for an agent's), then wakes that conversation. Each step is an activity with its own retries, so a
// delivery that fails is retried, and one that cannot be made fails the workflow where it can be seen.
func FireWorkflow(ctx workflow.Context, in FireInput) error {
	scheduled, ok := workflow.GetTypedSearchAttributes(ctx).GetTime(saScheduledStart)
	if !ok {
		scheduled = workflow.GetInfo(ctx).WorkflowStartTime // started by hand, not by a Schedule
	}
	opts := workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, BackoffCoefficient: 2, MaximumInterval: time.Minute, MaximumAttempts: 6}}
	ctx = workflow.WithActivityOptions(ctx, opts)
	var res engine.FireResult
	if err := workflow.ExecuteActivity(ctx, "Fire", FireScheduled{ItemID: in.ItemID, Scheduled: scheduled}).Get(ctx, &res); err != nil {
		return err
	}
	if res.Wake == nil {
		workflow.GetLogger(ctx).Info("alert handled", "item", in.ItemID, "fired", res.Fired, "skip", res.Skip)
		return nil
	}
	return workflow.ExecuteActivity(ctx, "WakeSession", *res.Wake).Get(ctx, nil)
}

// Activities carry out a firing.
type Activities struct {
	Service *engine.Service
	Client  client.Client
	Cfg     Config
}

func (a *Activities) Fire(ctx context.Context, in FireScheduled) (engine.FireResult, error) {
	return a.Service.Fire(ctx, in.ItemID, in.Scheduled)
}

// WakeSession signals the conversation's coordinator, starting it if it has gone idle. The coordinator turns the wake into
// the agent's own receive_wake step (agent-harness ADR 0002); this activity only hands it over.
func (a *Activities) WakeSession(ctx context.Context, w engine.FireWake) error {
	_, err := a.Client.SignalWithStartWorkflow(ctx, w.SessionKey, "Wake",
		map[string]any{"wake_id": w.WakeID, "objective": w.Objective, "why": w.Why},
		client.StartWorkflowOptions{
			ID: w.SessionKey, TaskQueue: a.Cfg.CoordinatorTaskQueue,
			// A coordinator exits on idle, and a closed id is only reusable when this says so.
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		},
		"CoordinatorWorkflow", map[string]any{"session_key": w.SessionKey, "tenant_slug": a.Cfg.TenantSlug})
	return err
}

// Run starts the worker for the engine's task queue and returns when ctx ends.
func (k *Clock) Run(ctx context.Context, svc *engine.Service) error {
	w := worker.New(k.c, k.cfg.TaskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(FireWorkflow, workflow.RegisterOptions{Name: "FireWorkflow"})
	acts := &Activities{Service: svc, Client: k.c, Cfg: k.cfg}
	w.RegisterActivity(acts)
	if err := w.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	w.Stop()
	return nil
}
