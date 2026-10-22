// Package clock is the engine's clock (project-hub ADR 0008): it arms Temporal Schedules for items' alerts, and runs the
// worker that carries out a Schedule's firing. Nothing outside the engine creates these Schedules.
package clock

import (
	"context"
	"errors"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// Search attributes the namespace already registers (the tenant's Schedules are found by them).
var (
	saTenant  = temporal.NewSearchAttributeKeyKeyword("TenantSlug")
	saKind    = temporal.NewSearchAttributeKeyKeyword("WakeKind")
	saSubject = temporal.NewSearchAttributeKeyKeyword("WakeSubject")
	saVisible = temporal.NewSearchAttributeKeyBool("WakeUserVisible")
)

type Config struct {
	Address, Namespace string
	// TaskQueue is the engine's own: the worker here polls it, and a Schedule's firing is started on it.
	TaskQueue string
	// CoordinatorTaskQueue is where the harness's session coordinators run; an agent's alert signals one there.
	CoordinatorTaskQueue string
	TenantSlug           string
}

type Clock struct {
	c   client.Client
	cfg Config
}

func New(cfg Config) (*Clock, error) {
	c, err := client.NewLazyClient(client.Options{HostPort: cfg.Address, Namespace: cfg.Namespace})
	if err != nil {
		return nil, err
	}
	return &Clock{c: c, cfg: cfg}, nil
}

func (k *Clock) Close() { k.c.Close() }

// Client is the Temporal client, for the worker.
func (k *Clock) Client() client.Client { return k.c }

func scheduleID(itemID, name string) string { return "life:" + itemID + ":" + name }

// FireInput is what a Schedule's firing starts the workflow with.
type FireInput struct {
	ItemID string `json:"item_id"`
	Name   string `json:"name"`
}

// calendar translates a spec's wall-clock times into Temporal's.
func scheduleSpec(sp engine.AlertSpec) client.ScheduleSpec {
	out := client.ScheduleSpec{StartAt: sp.Start, TimeZoneName: sp.TimeZone}
	rng := func(n int) []client.ScheduleRange { return []client.ScheduleRange{{Start: n}} }
	switch {
	case sp.Once != nil:
		// An exact instant is a calendar spec for that second (UTC). An interval spec cannot do it: intervals align to the
		// epoch, not to StartAt, so "start at T, every 10 years" has no occurrence inside [T, T+1s].
		t := sp.Once.UTC()
		out.TimeZoneName = "UTC"
		out.Calendars = []client.ScheduleCalendarSpec{{Year: rng(t.Year()), Month: rng(int(t.Month())), DayOfMonth: rng(t.Day()),
			Hour: rng(t.Hour()), Minute: rng(t.Minute()), Second: rng(t.Second())}}
	case sp.Calendar != nil:
		c := client.ScheduleCalendarSpec{Hour: rng(sp.Calendar.Hour), Minute: rng(sp.Calendar.Minute), Second: rng(sp.Calendar.Second)}
		for _, d := range sp.Calendar.Weekdays {
			c.DayOfWeek = append(c.DayOfWeek, client.ScheduleRange{Start: d})
		}
		if sp.Calendar.DayOfMonth != 0 {
			c.DayOfMonth = rng(sp.Calendar.DayOfMonth)
		}
		out.Calendars = []client.ScheduleCalendarSpec{c}
	case sp.Interval != nil:
		out.Intervals = []client.ScheduleIntervalSpec{{Every: sp.Interval.Every, Offset: sp.Interval.Offset}}
		out.TimeZoneName = ""
	}
	return out
}

func (k *Clock) action(id, itemID, name string) *client.ScheduleWorkflowAction {
	return &client.ScheduleWorkflowAction{
		ID: id, Workflow: "FireWorkflow", TaskQueue: k.cfg.TaskQueue,
		Args:                     []any{FireInput{ItemID: itemID, Name: name}},
		WorkflowExecutionTimeout: 10 * time.Minute,
	}
}

// catchup is how long after its time a firing is still made if the engine was down; longer and it is skipped, not made late.
const catchup = 10 * time.Minute

// Apply creates the Schedule, or updates it in place if it exists.
func (k *Clock) Apply(ctx context.Context, itemID string, sp engine.AlertSpec) error {
	id := scheduleID(itemID, sp.Name)
	spec := scheduleSpec(sp)
	remaining := 0 // unlimited for a repeating series
	if sp.Once != nil {
		remaining = 1
	}
	handle := k.c.ScheduleClient().GetHandle(ctx, id)
	err := handle.Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
		s := in.Description.Schedule
		s.Spec = &spec
		s.Action = k.action(id, itemID, sp.Name)
		s.Policy = &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: catchup}
		s.State = &client.ScheduleState{LimitedActions: remaining > 0, RemainingActions: remaining}
		return &client.ScheduleUpdate{Schedule: &s}, nil
	}})
	var nf *serviceerror.NotFound
	if err == nil || !errors.As(err, &nf) {
		return err
	}
	_, err = k.c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: id, Spec: spec, Action: k.action(id, itemID, sp.Name),
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: catchup,
		RemainingActions: remaining,
		TypedSearchAttributes: temporal.NewSearchAttributes(saTenant.ValueSet(k.cfg.TenantSlug), saKind.ValueSet("item"),
			saSubject.ValueSet("item:"+itemID), saVisible.ValueSet(true)),
	})
	return err
}

func (k *Clock) Remove(ctx context.Context, itemID, name string) error {
	err := k.c.ScheduleClient().GetHandle(ctx, scheduleID(itemID, name)).Delete(ctx)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

func (k *Clock) Exists(ctx context.Context, itemID, name string) (bool, error) {
	_, err := k.c.ScheduleClient().GetHandle(ctx, scheduleID(itemID, name)).Describe(ctx)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return false, nil
	}
	return err == nil, err
}
