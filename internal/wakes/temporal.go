// Package wakes arms Temporal Schedules the way the harness's arm_wake does (tenant-worker/tools_wake.py), so a wake the
// engine creates for the user and one the agent creates for itself are the same mechanism. The shape here is pinned by
// TestScheduleShape; change both sides together.
package wakes

import (
	"context"
	"errors"

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

func (t *Temporal) session(key string) string {
	if key == "" {
		return t.cfg.SessionKey
	}
	return key
}

func (t *Temporal) id(session, name string) string { return "wake:" + t.session(session) + ":" + name }

func (t *Temporal) Arm(ctx context.Context, w engine.Wake) error {
	_ = t.Cancel(ctx, w.SessionKey, w.Name) // replace: a schedule's spec is cheaper to recreate than to diff
	_, err := t.c.ScheduleClient().Create(ctx, t.options(w))
	return err
}

func (t *Temporal) options(w engine.Wake) client.ScheduleOptions {
	id := t.id(w.SessionKey, w.Name)
	return client.ScheduleOptions{
		ID: id,
		// A one-shot at an exact instant is a calendar spec for that second (UTC). An interval spec cannot do it: intervals
		// align to the epoch, not to StartAt, so "start at T, every 10 years" has no occurrence inside [T, T+1s].
		Spec: client.ScheduleSpec{
			Calendars: []client.ScheduleCalendarSpec{{
				Year:       []client.ScheduleRange{{Start: w.At.UTC().Year()}},
				Month:      []client.ScheduleRange{{Start: int(w.At.UTC().Month())}},
				DayOfMonth: []client.ScheduleRange{{Start: w.At.UTC().Day()}},
				Hour:       []client.ScheduleRange{{Start: w.At.UTC().Hour()}},
				Minute:     []client.ScheduleRange{{Start: w.At.UTC().Minute()}},
				Second:     []client.ScheduleRange{{Start: w.At.UTC().Second()}},
			}},
			TimeZoneName: "UTC",
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        id,
			Workflow:  "WakeWorkflow",
			TaskQueue: t.cfg.TaskQueue,
			Args: []any{map[string]any{
				"wake_id": id, "session_key": t.session(w.SessionKey), "tenant_slug": t.cfg.TenantSlug,
				"objective": w.Objective, "why": w.Why,
			}},
		},
		Overlap:          enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		RemainingActions: 1,
		TypedSearchAttributes: temporal.NewSearchAttributes(
			saTenant.ValueSet(t.cfg.TenantSlug), saKind.ValueSet(w.Kind), saSubject.ValueSet(w.Subject), saVisible.ValueSet(true)),
	}
}

func (t *Temporal) Cancel(ctx context.Context, session, name string) error {
	err := t.c.ScheduleClient().GetHandle(ctx, t.id(session, name)).Delete(ctx)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

func (t *Temporal) Exists(ctx context.Context, session, name string) (bool, error) {
	_, err := t.c.ScheduleClient().GetHandle(ctx, t.id(session, name)).Describe(ctx)
	var nf *serviceerror.NotFound
	if errors.As(err, &nf) {
		return false, nil
	}
	return err == nil, err
}
