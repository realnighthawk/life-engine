package clock

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/realnighthawk/life-engine/internal/engine"
	"github.com/realnighthawk/life-engine/internal/store/postgres"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
)

// Needs a real Temporal (with TenantSlug, WakeKind, WakeSubject and WakeUserVisible registered as search attributes on the
// namespace) and Postgres. It proves what the unit tests cannot: that a Schedule built from an item's alert fires, starts
// the workflow, and ends in a notice or a signal to the coordinator.
//
//	LIVE_TEMPORAL_ADDRESS=127.0.0.1:57233 TEST_DATABASE_URL=... go test ./internal/clock -run Live -v
func liveEnv(t *testing.T) (*engine.Service, *Clock, *pgxpool.Pool, context.Context) {
	t.Helper()
	addr, dsn := os.Getenv("LIVE_TEMPORAL_ADDRESS"), os.Getenv("TEST_DATABASE_URL")
	if addr == "" || dsn == "" {
		t.Skip("LIVE_TEMPORAL_ADDRESS and TEST_DATABASE_URL are not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, postgres.Migrate(ctx, pool))
	_, err = pool.Exec(ctx, "TRUNCATE life.requests, life.changes, life.acked_events, life.notices, life.occurrences, life.items, life.goals, life.views, life.measurements")
	require.NoError(t, err)
	queue := "life-engine-live-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	k, err := New(Config{Address: addr, Namespace: "default", TaskQueue: queue, CoordinatorTaskQueue: queue + "-coordinator", TenantSlug: "live"})
	require.NoError(t, err)
	t.Cleanup(k.Close)
	svc := engine.New(pool)
	svc.SetWaker(ctx, k, time.Second)
	go func() { _ = k.Run(ctx, svc) }()
	return svc, k, pool, ctx
}

func run(t *testing.T, ctx context.Context, s *engine.Service, op string, body map[string]any) json.RawMessage {
	t.Helper()
	if _, ok := body["idempotency_key"]; !ok {
		body["idempotency_key"] = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	raw, _ := json.Marshal(body)
	out, err := s.Execute(ctx, op, raw)
	require.NoError(t, err)
	return out
}

func TestLiveAPersonsAlertFiresIntoANotice(t *testing.T) {
	svc, _, pool, ctx := liveEnv(t)
	at := time.Now().UTC().Add(8 * time.Second).Truncate(time.Second)
	var task engine.Item
	require.NoError(t, json.Unmarshal(run(t, ctx, svc, "tasks_create", map[string]any{"title": "Call Sam", "at": at.Format(time.RFC3339), "alert_before_minutes": 0}), &task))

	require.Eventually(t, func() bool {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM life.notices WHERE link_id=$1", task.ID).Scan(&n)
		return n == 1
	}, 40*time.Second, 500*time.Millisecond, "the alert should have become a notice")
}

func TestLiveAnAgentsAlertSignalsItsCoordinator(t *testing.T) {
	svc, k, _, ctx := liveEnv(t)
	at := time.Now().UTC().Add(8 * time.Second).Truncate(time.Second)
	session := "agent:main:ios:user:live-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	raw, _ := json.Marshal(map[string]any{"idempotency_key": "k" + session, "title": "Check the deploy", "subject": "agent", "at": at.Format(time.RFC3339), "alert_before_minutes": 0})
	_, err := svc.Execute(engine.WithSession(ctx, session+":session:s1"), "tasks_create", raw)
	require.NoError(t, err)

	// The coordinator has no worker here, so it is started and holds the Wake signal in its history.
	require.Eventually(t, func() bool {
		iter := k.Client().GetWorkflowHistory(ctx, session, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for iter.HasNext() {
			ev, err := iter.Next()
			if err != nil {
				return false
			}
			if s := ev.GetWorkflowExecutionSignaledEventAttributes(); s != nil && s.SignalName == "Wake" {
				return true
			}
		}
		return false
	}, 40*time.Second, time.Second, "the coordinator should have been signalled")
}

func TestLiveARepeatKeepsLocalWallClockTime(t *testing.T) {
	svc, k, _, ctx := liveEnv(t)
	london, _ := time.LoadLocation("Europe/London")
	anchor := time.Date(2026, 1, 5, 9, 0, 0, 0, london) // 09:00 in winter; the alert is 30 minutes before
	var task engine.Item
	require.NoError(t, json.Unmarshal(run(t, ctx, svc, "tasks_create", map[string]any{"title": "Standup", "at": anchor.UTC().Format(time.RFC3339),
		"time_zone": "Europe/London", "alert_before_minutes": 30, "recurrence": map[string]any{"freq": "daily"}}), &task), "create")

	var next time.Time
	require.Eventually(t, func() bool {
		d, err := k.c.ScheduleClient().GetHandle(ctx, scheduleID(task.ID, "main")).Describe(ctx)
		if err != nil || len(d.Info.NextActionTimes) == 0 {
			return false
		}
		next = d.Info.NextActionTimes[0]
		return true
	}, 20*time.Second, 500*time.Millisecond)
	local := next.In(london)
	require.Equal(t, 8, local.Hour(), "next alert %s", local)
	require.Equal(t, 30, local.Minute())
	require.True(t, next.After(time.Now()))
}
