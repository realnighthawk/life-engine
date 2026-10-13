package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realnighthawk/life-engine/internal/engine"
)

// TestWriteClientFixtures records real engine responses for the client apps to decode in their own tests (the iOS app
// keeps a copy under Tests/Fixtures/life). It writes nothing unless LIFE_WRITE_FIXTURES names a directory, so it is a
// no-op in CI, but it always runs the scenario and checks every response is a JSON object, which is the contract.
//
//	LIFE_WRITE_FIXTURES=$PWD/fixtures TEST_DATABASE_URL=... go test ./internal/engine -run TestWriteClientFixtures
func TestWriteClientFixtures(t *testing.T) {
	s := integrationService(t)
	dir := os.Getenv("LIFE_WRITE_FIXTURES")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	save := func(name string, raw []byte) {
		t.Helper()
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			t.Fatalf("%s: a response must be a JSON object: %v", name, err)
		}
		if dir == "" {
			return
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		if err := os.WriteFile(filepath.Join(dir, name+".json"), pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name, op string, body any) map[string]any {
		t.Helper()
		in, _ := json.Marshal(body)
		raw, err := s.Execute(context.Background(), op, in)
		if err != nil {
			t.Fatalf("%s (%s): %v", name, op, engine.PublicError(err))
		}
		save(name, raw)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	fail := func(name, op string, body any) {
		t.Helper()
		in, _ := json.Marshal(body)
		_, err := s.Execute(context.Background(), op, in)
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		raw, _ := json.Marshal(map[string]any{"error": engine.PublicError(err)})
		save(name, raw)
	}
	id := func(m map[string]any) string { return m["id"].(string) }
	k := func() string { return key() }

	call("settings", "settings_set", map[string]any{"idempotency_key": k(), "expected_version": 1, "time_zone": "Europe/London", "work_start": "09:00", "work_end": "17:30"})

	rem := call("reminder", "reminders_create", map[string]any{"idempotency_key": k(), "id": "11111111-1111-4111-8111-111111111111", "title": "Pay rent", "notes": "Landlord prefers a bank transfer",
		"due_at": "2030-01-15T09:00:00Z", "time_zone": "Europe/London", "source": "agent", "session_id": "agent:main:ios:user:u", "turn_seq": 7, "link": map[string]any{"entity": "goal", "id": "g1"}})
	call("reminder_repeating", "reminders_create", map[string]any{"idempotency_key": k(), "id": "11111111-1111-4111-8111-222222222222", "title": "Take vitamins", "due_at": "2030-01-15T08:00:00Z",
		"time_zone": "Europe/London", "recurrence": map[string]any{"freq": "weekly", "weekdays": []int{1, 3, 5}}})
	call("reminder_undated", "reminders_create", map[string]any{"idempotency_key": k(), "id": "11111111-1111-4111-8111-333333333333", "title": "Someday: book a trip"})
	call("reminders_page", "reminders_list", map[string]any{})
	call("reminder_completed", "reminders_complete", map[string]any{"idempotency_key": k(), "id": id(rem), "expected_version": 1})

	call("schedule", "schedules_create", map[string]any{"idempotency_key": k(), "id": "22222222-2222-4222-8222-222222222222", "name": "Weekly review", "instruction": "Review my week and suggest one change.",
		"time_of_day": "18:00", "recurrence": map[string]any{"freq": "weekly", "weekdays": []int{0}}})
	call("schedules_page", "schedules_list", map[string]any{})

	goal := call("goal", "goals_create", map[string]any{"idempotency_key": k(), "id": "33333333-3333-4333-8333-333333333333", "title": "Learn Spanish", "why": "Trip in spring", "weekly_target": 4,
		"steps": []map[string]any{{"title": "Pick a course", "minutes": 10}, {"title": "First lesson", "minutes": 25}}})
	steps := goal["steps"].([]any)
	call("step_completed", "steps_complete", map[string]any{"idempotency_key": k(), "id": steps[0].(map[string]any)["id"], "expected_version": 1, "done": true})
	call("goals_page", "goals_list", map[string]any{})
	call("commitments_week", "commitments_add", map[string]any{"idempotency_key": k(), "id": "44444444-4444-4444-8444-444444444444", "step_id": steps[1].(map[string]any)["id"]})
	call("commitments_list", "commitments_list", map[string]any{})

	memo := call("plan_item", "plan_items_create", map[string]any{"idempotency_key": k(), "id": "55555555-5555-4555-8555-555555555555", "title": "Draft memo", "minutes": 40, "is_priority": true,
		"goal_id": id(goal), "start_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)})
	call("plan_item_event", "plan_items_create", map[string]any{"idempotency_key": k(), "title": "Standup", "kind": "event", "minutes": 15})
	call("plan_item_linked", "plan_items_create", map[string]any{"idempotency_key": k(), "title": "Spanish lesson", "minutes": 25, "step_id": steps[1].(map[string]any)["id"]})
	call("plan_proposal", "plan_propose", map[string]any{"idempotency_key": k(), "id": "66666666-6666-4666-8666-666666666666", "day": time.Now().UTC().Format("2006-01-02"), "reason": "Your afternoon meeting ran long",
		"changes": []map[string]any{{"action": "move", "item_id": id(memo), "day": time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")}, {"action": "add", "title": "Take a walk", "kind": "rest", "minutes": 15}}})
	call("plan_day", "plan_get", map[string]any{})
	call("replan", "plan_replan_preview", map[string]any{"minutes_left": 30})
	call("next_action", "next_action", map[string]any{"minutes": 30})
	call("next_action_none", "next_action", map[string]any{"minutes": 1})

	routine := call("routine", "routines_create", map[string]any{"idempotency_key": k(), "id": "77777777-7777-4777-8777-777777777777", "title": "Morning run", "minutes": 30, "short_title": "Walk around the block",
		"short_minutes": 10, "weekdays": []int{1, 2, 3, 4, 5}, "cues": []string{"07:30"}, "target_per_day": 1})
	call("routine_status", "routine_checkins_set", map[string]any{"idempotency_key": k(), "routine_id": id(routine), "variant": "short"})
	call("routines_status", "routines_status", map[string]any{})
	call("goal_linked", "goals_link_routine", map[string]any{"idempotency_key": k(), "goal_id": id(goal), "routine_id": id(routine)})
	water := call("routine_counted", "routines_create", map[string]any{"idempotency_key": k(), "id": "78787878-7878-4787-8787-787878787878", "title": "Drink water", "target_per_day": 3,
		"cues": []string{"09:00", "13:00", "17:00"}, "short_title": "One glass", "minutes": 2, "short_minutes": 1})
	call("routine_log", "routines_log", map[string]any{"idempotency_key": k(), "routine_id": id(water)})
	call("routine_history", "routines_history", map[string]any{"routine_id": id(water), "days": 14})

	call("journal_entry", "journal_create", map[string]any{"idempotency_key": k(), "id": "88888888-8888-4888-8888-888888888888", "text": "Went for a run and felt strong.", "goal_ids": []string{id(goal)}, "tomorrow": "Stretch before bed", "source": "agent", "session_id": "agent:main:ios:user:u", "turn_seq": 9})
	call("journal_page", "journal_list", map[string]any{"search": "run"})
	note := call("note", "notes_create", map[string]any{"idempotency_key": k(), "id": "99999999-9999-4999-8999-999999999999", "title": "Packing list", "body": "- passport\n- charger", "tags": []string{"travel", "lists"}, "pinned": true})
	call("notes_page", "notes_list", map[string]any{})
	call("capture", "captures_create", map[string]any{"idempotency_key": k(), "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "text": "remind me to book the dentist", "source": "voice"})
	call("captures_page", "captures_list", map[string]any{})
	call("attachment_reserved", "attachments_upload_init", map[string]any{"idempotency_key": k(), "owner_type": "note", "owner_id": id(note), "filename": "passport.jpg", "content_type": "image/jpeg", "size_bytes": 12345})

	call("history_page", "history_list", map[string]any{"entity_type": "reminder", "id": id(rem)})
	call("life_context_day", "life_context_get", map[string]any{"scope": "day"})
	call("week_stats", "week_stats", map[string]any{})
	call("day_close", "day_close", map[string]any{"idempotency_key": k(), "reflection": "Good focus in the morning, then meetings ate the afternoon."})

	// A reminder that is due, as the notice mcp-hub drains.
	call("reminder_due", "reminders_create", map[string]any{"idempotency_key": k(), "title": "Call the bank", "due_at": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), "time_zone": "Europe/London"})
	call("reminders_agenda", "reminders_agenda", map[string]any{})
	call("reminder_acked", "reminders_ack", map[string]any{"idempotency_key": k(), "id": id(rem)})
	pending := call("events_pending", "events_pending", map[string]any{})
	if items := pending["items"].([]any); len(items) == 0 {
		t.Fatal("expected a due notice")
	}
	call("events_ack", "events_ack", map[string]any{"event_ids": []string{pending["items"].([]any)[0].(map[string]any)["event_id"].(string)}})
	call("capabilities_ok", "settings_get", map[string]any{})

	fail("error_conflict", "reminders_update", map[string]any{"idempotency_key": k(), "id": id(rem), "expected_version": 1, "title": "Stale edit"})
	fail("error_invalid", "reminders_create", map[string]any{"idempotency_key": k(), "title": "   "})
	fail("error_not_found", "reminders_get", map[string]any{"id": "ffffffff-ffff-4fff-8fff-ffffffffffff"})
}
