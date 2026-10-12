// Package wakes arms Temporal Schedules the way the harness's arm_wake does (tenant-worker/tools_wake.py), so a wake the
// engine creates for the user and one the agent creates for itself are the same mechanism. The shape here is pinned by
// TestScheduleShape; change both sides together.
package wakes

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

var (
	saTenant  = temporal.NewSearchAttributeKeyKeyword("TenantSlug")
	saKind    = temporal.NewSearchAttributeKeyKeyword("WakeKind")
	saSubject = temporal.NewSearchAttributeKeyKeyword("WakeSubject")
	saVisible = temporal.NewSearchAttributeKeyBool("WakeUserVisible")
)

type Config struct {
	Address, Namespace, TaskQueue, TenantSlug string
	// SessionKey is the session a fired wake wakes, e.g. agent:main:ios:user:<clerk id>.
	SessionKey string
}

type Temporal struct {
	c   client.Client
	cfg Config
}

func New(cfg Config) (*Temporal, error) {
	c, err := client.NewLazyClient(client.Options{HostPort: cfg.Address, Namespace: cfg.Namespace})
	if err != nil {
		return nil, err
	}
	return &Temporal{c: c, cfg: cfg}, nil
}

func (t *Temporal) Close() { t.c.Close() }

func (t *Temporal) id(name string) string { return "wake:" + t.cfg.SessionKey + ":" + name }

func (t *Temporal) Arm(ctx context.Context, w engine.Wake) error {
	_ = t.Cancel(ctx, w.Name) // replace: a schedule's spec is cheaper to recreate than to diff
	_, err := t.c.ScheduleClient().Create(ctx, t.options(w))
	return err
}

func (t *Temporal) options(w engine.Wake) client.ScheduleOptions {
	id := t.id(w.Name)
	return client.ScheduleOptions{
		ID: id,
		// A one-shot, exactly as arm_wake builds it: start at the instant, one interval, closed a second later.
		Spec: client.ScheduleSpec{
			StartAt:   w.At,
			EndAt:     w.At.Add(time.Second),
			Intervals: []client.ScheduleIntervalSpec{{Every: 3650 * 24 * time.Hour}},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        id,
			Workflow:  "WakeWorkflow",
			TaskQueue: t.cfg.TaskQueue,
			Args: []any{map[string]any{
				"wake_id": id, "session_key": t.cfg.SessionKey, "tenant_slug": t.cfg.TenantSlug,
				"objective": w.Objective, "why": w.Why,
			}},
		},
		Overlap:          enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		RemainingActions: 1,
		TypedSearchAttributes: temporal.NewSearchAttributes(
			saTenant.ValueSet(t.cfg.TenantSlug), saKind.ValueSet(w.Kind), saSubject.ValueSet(w.Subject), saVisible.ValueSet(true)),
	}
}

func (t *Temporal) Cancel(ctx context.Context, name string) error {
	err := t.c.ScheduleClient().GetHandle(ctx, t.id(name)).Delete(ctx)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}
