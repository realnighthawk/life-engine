package clock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestAnExactInstantIsACalendarSpecForThatSecond(t *testing.T) {
	at := time.Date(2026, 10, 12, 17, 5, 9, 0, time.UTC)
	sp := scheduleSpec(engine.AlertSpec{Name: "main", TimeZone: "Europe/London", Once: &at, Start: at})
	require.Equal(t, "UTC", sp.TimeZoneName) // an instant is read in UTC whatever the item's zone
	require.Len(t, sp.Calendars, 1)
	c := sp.Calendars[0]
	require.Equal(t, 2026, c.Year[0].Start)
	require.Equal(t, 10, c.Month[0].Start)
	require.Equal(t, 12, c.DayOfMonth[0].Start)
	require.Equal(t, 17, c.Hour[0].Start)
	require.Equal(t, 5, c.Minute[0].Start)
	require.Equal(t, 9, c.Second[0].Start)
}

func TestARepeatKeepsItsZoneAndWeekdays(t *testing.T) {
	start := time.Date(2026, 10, 12, 7, 30, 0, 0, time.UTC)
	sp := scheduleSpec(engine.AlertSpec{Name: "main", TimeZone: "Europe/London", Start: start,
		Calendar: &engine.CalendarSpec{Hour: 8, Minute: 30, Weekdays: []int{1, 3}}})
	require.Equal(t, "Europe/London", sp.TimeZoneName)
	require.Equal(t, start, sp.StartAt)
	c := sp.Calendars[0]
	require.Equal(t, 8, c.Hour[0].Start)
	require.Equal(t, 30, c.Minute[0].Start)
	require.Len(t, c.DayOfWeek, 2)
	require.Equal(t, 3, c.DayOfWeek[1].Start)
	require.Empty(t, c.DayOfMonth) // unset reads as every day

	monthly := scheduleSpec(engine.AlertSpec{Name: "main", TimeZone: "UTC", Calendar: &engine.CalendarSpec{Hour: 9, DayOfMonth: 15}})
	require.Equal(t, 15, monthly.Calendars[0].DayOfMonth[0].Start)

	every := scheduleSpec(engine.AlertSpec{Name: "main", TimeZone: "UTC", Interval: &engine.IntervalSpec{Every: 48 * time.Hour, Offset: time.Hour}})
	require.Equal(t, []time.Duration{48 * time.Hour, time.Hour}, []time.Duration{every.Intervals[0].Every, every.Intervals[0].Offset})
	require.Empty(t, every.Calendars)
}

func TestTheActionStartsFireWorkflowOnTheEnginesOwnQueue(t *testing.T) {
	k := &Clock{cfg: Config{TaskQueue: "life-engine"}}
	a := k.action("life:i1:main", "i1", "main")
	require.Equal(t, "FireWorkflow", a.Workflow)
	require.Equal(t, "life-engine", a.TaskQueue)
	require.Equal(t, FireInput{ItemID: "i1", Name: "main"}, a.Args[0])
}

func fireEnv(t *testing.T, fire func(FireScheduled) (engine.FireResult, error), wake func(engine.FireWake) error, scheduled time.Time) *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, in FireScheduled) (engine.FireResult, error) { return fire(in) }, activity.RegisterOptions{Name: "Fire"})
	env.RegisterActivityWithOptions(func(_ context.Context, w engine.FireWake) error { return wake(w) }, activity.RegisterOptions{Name: "WakeSession"})
	env.SetStartTime(scheduled)
	env.SetTypedSearchAttributesOnStart(temporal.NewSearchAttributes(saScheduledStart.ValueSet(scheduled)))
	return env
}

func TestAPersonsAlertFiresInTheEngineAndWakesNoOne(t *testing.T) {
	at := time.Date(2026, 10, 12, 8, 50, 0, 0, time.UTC)
	var fired FireScheduled
	woke := false
	env := fireEnv(t, func(in FireScheduled) (engine.FireResult, error) {
		fired = in
		return engine.FireResult{Fired: true}, nil
	},
		func(engine.FireWake) error { woke = true; return nil }, at)
	env.ExecuteWorkflow(FireWorkflow, FireInput{ItemID: "i1", Name: "main"})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, FireScheduled{ItemID: "i1", Scheduled: at}, fired) // the occurrence is the scheduled instant
	require.False(t, woke)
}

func TestAnAgentsAlertWakesItsConversation(t *testing.T) {
	var got engine.FireWake
	env := fireEnv(t, func(FireScheduled) (engine.FireResult, error) {
		return engine.FireResult{Fired: true, Wake: &engine.FireWake{SessionKey: "agent:main:ios:user:u1", WakeID: "item:i1@1", Objective: "Check the deploy"}}, nil
	}, func(w engine.FireWake) error { got = w; return nil }, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	env.ExecuteWorkflow(FireWorkflow, FireInput{ItemID: "i1", Name: "main"})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "agent:main:ios:user:u1", got.SessionKey)
	require.Equal(t, "Check the deploy", got.Objective)
}

func TestADeliveryThatKeepsFailingFailsTheWorkflowWhereItCanBeSeen(t *testing.T) {
	calls := 0
	env := fireEnv(t, func(FireScheduled) (engine.FireResult, error) {
		return engine.FireResult{Fired: true, Wake: &engine.FireWake{SessionKey: "s", WakeID: "w", Objective: "o"}}, nil
	}, func(engine.FireWake) error { calls++; return errors.New("coordinator unreachable") }, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	env.ExecuteWorkflow(FireWorkflow, FireInput{ItemID: "i1", Name: "main"})
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, 6, calls) // retried, then given up on loudly
}
