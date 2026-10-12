package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestJournalAndSearch(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Run a 10k"})
	j := execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "Went for a run along the river and felt strong. Decided to enter the Lisbon half marathon.",
		"goal_ids": []string{g.ID}, "tomorrow": "Stretch before bed", "source": "agent", "session_id": "s-1", "turn_seq": 4})
	if j.Source != "agent" || len(j.GoalIDs) != 1 || *j.Tomorrow != "Stretch before bed" || j.Version != 1 || len(j.Attachments) != 0 {
		t.Fatalf("created: %+v", j)
	}
	execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "Slow day, mostly meetings. Tired by four."})
	errorCode(t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "x", "goal_ids": []string{uuid.NewString()}}, "invalid_input")
	errorCode(t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "   "}, "invalid_input")

	// Search finds stemmed words and literal fragments, newest first.
	for query, want := range map[string]int{"running": 1, "Lisbon": 1, "marathon": 1, "meet": 1, "tired": 1, "run": 1, "nonexistentword": 0, "": 2} {
		if got := execute[engine.Page[engine.JournalEntry]](t, s, "journal_list", map[string]any{"search": query}); got.Total != want {
			t.Errorf("search %q: got %d want %d", query, got.Total, want)
		}
	}
	if got := execute[engine.Page[engine.JournalEntry]](t, s, "journal_list", map[string]any{"goal_id": g.ID}); got.Total != 1 || got.Items[0].ID != j.ID {
		t.Fatalf("by goal: %+v", got.Total)
	}
	if got := execute[engine.Page[engine.JournalEntry]](t, s, "journal_list", map[string]any{"from": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}); got.Total != 0 {
		t.Fatalf("from: %d", got.Total)
	}
	u := execute[engine.JournalEntry](t, s, "journal_update", map[string]any{"idempotency_key": key(), "id": j.ID, "expected_version": 1, "text": "Edited text about the river run."})
	if u.Version != 2 || u.Source != "user" {
		t.Fatalf("updated: %+v", u)
	}
	errorCode(t, s, "journal_update", map[string]any{"idempotency_key": key(), "id": j.ID, "expected_version": 1, "text": "stale"}, "conflict")
	execute[engine.JournalEntry](t, s, "journal_delete", map[string]any{"idempotency_key": key(), "id": j.ID, "expected_version": 2})
	if got := execute[engine.Page[engine.JournalEntry]](t, s, "journal_list", map[string]any{}); got.Total != 1 {
		t.Fatalf("deleted still listed: %d", got.Total)
	}
}

func TestNotes(t *testing.T) {
	s := integrationService(t)
	n := execute[engine.Note](t, s, "notes_create", map[string]any{"idempotency_key": key(), "title": "Packing list", "body": "- passport\n- charger\n- sunscreen", "tags": []string{"Travel", " travel ", "lists"}, "pinned": true})
	if len(n.Tags) != 2 || n.Tags[0] != "lists" || n.Tags[1] != "travel" || !n.Pinned {
		t.Fatalf("tags normalised: %+v", n.Tags)
	}
	execute[engine.Note](t, s, "notes_create", map[string]any{"idempotency_key": key(), "title": "Gift ideas", "body": "Book for Sam"})
	if got := execute[engine.Page[engine.Note]](t, s, "notes_list", map[string]any{}); got.Total != 2 || got.Items[0].ID != n.ID {
		t.Fatalf("pinned first: %+v", got.Items[0].Title)
	}
	if got := execute[engine.Page[engine.Note]](t, s, "notes_list", map[string]any{"tag": "TRAVEL"}); got.Total != 1 {
		t.Fatalf("tag: %d", got.Total)
	}
	if got := execute[engine.Page[engine.Note]](t, s, "notes_list", map[string]any{"search": "sunscreen"}); got.Total != 1 {
		t.Fatalf("search body: %d", got.Total)
	}
	if got := execute[engine.Page[engine.Note]](t, s, "notes_list", map[string]any{"pinned": true}); got.Total != 1 {
		t.Fatalf("pinned filter: %d", got.Total)
	}
	errorCode(t, s, "notes_create", map[string]any{"idempotency_key": key(), "title": "x", "tags": []string{strings.Repeat("a", 41)}}, "invalid_input")
	upd := execute[engine.Note](t, s, "notes_update", map[string]any{"idempotency_key": key(), "id": n.ID, "expected_version": 1, "title": "Packing list v2", "body": "x"})
	if upd.Version != 2 || upd.Pinned {
		t.Fatalf("updated: %+v", upd)
	}
}

func TestCapturesAreSortedByTheAgent(t *testing.T) {
	s := integrationService(t)
	c := execute[engine.Capture](t, s, "captures_create", map[string]any{"idempotency_key": key(), "id": uuid.NewString(), "text": "remind me to book the dentist", "source": "voice"})
	// An offline retry under a new key with the same client id cannot duplicate.
	errorCode(t, s, "captures_create", map[string]any{"idempotency_key": key(), "id": c.ID, "text": "remind me to book the dentist"}, "duplicate")

	ev := execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items
	if len(ev) != 1 || ev[0].EventKey != "life:capture" || !strings.Contains(ev[0].Detail, "book the dentist") {
		t.Fatalf("capture notice: %+v", ev)
	}
	execute[engine.AckEventsResult](t, s, "events_ack", map[string]any{"event_ids": []string{ev[0].EventID}})
	if open := execute[engine.Page[engine.Capture]](t, s, "captures_list", map[string]any{}); open.Total != 1 {
		t.Fatalf("acked but unresolved capture stays open for the person: %d", open.Total)
	}
	if n := len(execute[engine.EventsResult](t, s, "events_pending", map[string]any{}).Items); n != 0 {
		t.Fatalf("capture notified twice: %d", n)
	}

	r := execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Book the dentist", "source": "agent"})
	errorCode(t, s, "captures_resolve", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 1, "as": "reminder"}, "invalid_input")
	errorCode(t, s, "captures_resolve", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 1, "as": "reminder", "entity_id": uuid.NewString()}, "invalid_input")
	done := execute[engine.Capture](t, s, "captures_resolve", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 1, "as": "reminder", "entity_id": r.ID})
	if done.State != "resolved" || done.ResolvedAs != "reminder" || done.ResolvedID != r.ID {
		t.Fatalf("resolved: %+v", done)
	}
	errorCode(t, s, "captures_resolve", map[string]any{"idempotency_key": key(), "id": c.ID, "expected_version": 2, "as": "dismissed"}, "conflict")
	if open := execute[engine.Page[engine.Capture]](t, s, "captures_list", map[string]any{}); open.Total != 0 {
		t.Fatalf("still open: %d", open.Total)
	}
	d := execute[engine.Capture](t, s, "captures_create", map[string]any{"idempotency_key": key(), "text": "asdf"})
	if x := execute[engine.Capture](t, s, "captures_resolve", map[string]any{"idempotency_key": key(), "id": d.ID, "expected_version": 1, "as": "dismissed"}); x.ResolvedAs != "dismissed" {
		t.Fatalf("dismissed: %+v", x)
	}
}

func TestDayCloseAndWeek(t *testing.T) {
	s := integrationService(t)
	d := today()
	next := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	done := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Send invoice", "minutes": 15})
	execute[engine.PlanItem](t, s, "plan_items_complete", map[string]any{"idempotency_key": key(), "id": done.ID, "expected_version": 1, "done": true})
	open := execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Draft memo", "minutes": 40, "is_priority": true})
	execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Standup", "kind": "event"})

	k := key()
	in := map[string]any{"idempotency_key": k, "reflection": "Good focus in the morning, then meetings ate the afternoon."}
	c := execute[engine.DayClose](t, s, "day_close", in)
	if c.Done != 1 || c.Planned != 2 || len(c.Carried) != 1 || c.Carried[0].ID != open.ID || c.TomorrowNote != `Start tomorrow with "Draft memo".` {
		t.Fatalf("closed: %+v", c)
	}
	// Retrying the same close replays; the carried item moved exactly once.
	again := execute[engine.DayClose](t, s, "day_close", in)
	if len(again.Carried) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	if p := execute[engine.PlanDay](t, s, "plan_get", map[string]any{"day": next}); len(p.Items) != 1 || p.Items[0].ID != open.ID {
		t.Fatalf("carried to tomorrow: %+v", p.Items)
	}
	if p := execute[engine.PlanDay](t, s, "plan_get", map[string]any{"day": d}); p.Summary.Done != 1 || p.Summary.Planned != 1 {
		t.Fatalf("today after close: %+v", p.Summary)
	}
	j := execute[engine.Page[engine.JournalEntry]](t, s, "journal_list", map[string]any{})
	if j.Total != 1 || j.Items[0].Source != "day_close" || j.Items[0].Tomorrow == nil {
		t.Fatalf("reflection saved: %+v", j.Items)
	}
	// Closing again with a new key moves nothing more.
	if second := execute[engine.DayClose](t, s, "day_close", map[string]any{"idempotency_key": key()}); len(second.Carried) != 0 {
		t.Fatalf("second close: %+v", second)
	}

	week := execute[engine.WeekStats](t, s, "week_stats", map[string]any{})
	if week.Planned < 1 || week.Done != 1 || week.JournalEntries != 1 || week.Change == "" {
		t.Fatalf("week: %+v", week)
	}
	errorCode(t, s, "week_stats", map[string]any{"week_start": "2026-10-14"}, "invalid_input")
}

func TestLifeContext(t *testing.T) {
	s := integrationService(t)
	g := execute[engine.Goal](t, s, "goals_create", map[string]any{"idempotency_key": key(), "title": "Write a book", "steps": []map[string]any{{"title": "Outline chapter 1", "minutes": 25}}})
	execute[engine.PlanItem](t, s, "plan_items_create", map[string]any{"idempotency_key": key(), "title": "Draft memo", "is_priority": true})
	execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Pay rent", "due_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	execute[engine.Reminder](t, s, "reminders_create", map[string]any{"idempotency_key": key(), "title": "Next month", "due_at": time.Now().AddDate(0, 1, 0).UTC().Format(time.RFC3339)})
	execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": strings.Repeat("long ", 400)})
	execute[engine.Routine](t, s, "routines_create", map[string]any{"idempotency_key": key(), "title": "Stretch"})

	day := execute[engine.LifeContext](t, s, "life_context_get", map[string]any{"scope": "day"})
	if day.SchemaVersion != 1 || day.Plan == nil || len(day.Plan.Items) != 1 || len(day.Goals) != 1 || day.Goals[0].NextStep == nil || day.Routines == nil || day.Commitments == nil {
		t.Fatalf("day: %+v", day)
	}
	if len(day.RemindersDue) != 1 || day.RemindersDue[0].Title != "Pay rent" {
		t.Fatalf("only reminders overdue or due within a day: %+v", day.RemindersDue)
	}
	if len(day.RecentJournal) != 1 || len([]rune(day.RecentJournal[0].Text)) > 501 {
		t.Fatalf("journal is truncated: %d", len([]rune(day.RecentJournal[0].Text)))
	}
	kinds := map[string]bool{}
	for _, src := range day.Sources {
		kinds[src.EntityType] = true
		if src.Version < 1 || src.ID == "" {
			t.Fatalf("source: %+v", src)
		}
	}
	for _, k := range []string{"goal", "plan_item", "reminder", "journal_entry"} {
		if !kinds[k] {
			t.Errorf("missing source kind %s", k)
		}
	}
	if wk := execute[engine.LifeContext](t, s, "life_context_get", map[string]any{"scope": "week"}); wk.Week == nil {
		t.Fatal("week scope has stats")
	}
	if gc := execute[engine.LifeContext](t, s, "life_context_get", map[string]any{"scope": "goal", "goal_id": g.ID}); len(gc.Goals) != 1 || gc.Plan != nil {
		t.Fatalf("goal scope: %+v", gc)
	}
	errorCode(t, s, "life_context_get", map[string]any{"scope": "goal"}, "invalid_input")
	errorCode(t, s, "life_context_get", map[string]any{"scope": "year"}, "invalid_input")
}

func TestAttachmentsLifecycle(t *testing.T) {
	s := integrationService(t)
	j := execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "Photo from the trail"})
	a := execute[engine.Attachment](t, s, "attachments_upload_init", map[string]any{"idempotency_key": key(), "owner_type": "journal_entry", "owner_id": j.ID, "filename": "../../etc/trail.jpg", "content_type": "image/jpeg", "size_bytes": 4})
	if a.State != "pending" || a.UploadPath == "" || a.Filename != "trail.jpg" {
		t.Fatalf("reserved: %+v", a)
	}
	// Pending attachments are not listed on the record until the bytes arrive.
	if got := execute[engine.JournalEntry](t, s, "journal_get", map[string]any{"id": j.ID}); len(got.Attachments) != 0 {
		t.Fatalf("pending listed: %+v", got.Attachments)
	}
	for name, body := range map[string]map[string]any{
		"bad type":  {"idempotency_key": key(), "owner_type": "note", "owner_id": j.ID, "filename": "a", "content_type": "application/x-sh", "size_bytes": 1},
		"too big":   {"idempotency_key": key(), "owner_type": "journal_entry", "owner_id": j.ID, "filename": "a", "content_type": "image/png", "size_bytes": 26 << 20},
		"no owner":  {"idempotency_key": key(), "owner_type": "journal_entry", "owner_id": uuid.NewString(), "filename": "a", "content_type": "image/png", "size_bytes": 1},
		"bad owner": {"idempotency_key": key(), "owner_type": "goal", "owner_id": j.ID, "filename": "a", "content_type": "image/png", "size_bytes": 1},
	} {
		raw := body
		if _, e := s.Execute(t.Context(), "attachments_upload_init", mustJSON(t, raw)); e == nil || engine.PublicError(e).Code != "invalid_input" {
			t.Errorf("%s: want invalid_input, got %v", name, e)
		}
	}

	up, err := s.Upload(t.Context(), a.ID, "image/jpeg", strings.NewReader("JFIF"))
	if err != nil || up.State != "ready" || up.ContentPath == "" || up.SHA256 == "" || up.Version != 2 {
		t.Fatalf("upload: %+v %v", up, err)
	}
	if _, err = s.Upload(t.Context(), a.ID, "image/jpeg", strings.NewReader("JFIF")); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if _, err = s.Upload(t.Context(), a.ID, "image/jpeg", strings.NewReader("JFIX")); engine.PublicError(err).Code != "conflict" {
		t.Fatalf("different bytes for a finished attachment: %v", err)
	}
	_, data, err := s.Download(t.Context(), a.ID)
	if err != nil || string(data) != "JFIF" {
		t.Fatalf("download: %q %v", data, err)
	}
	if got := execute[engine.JournalEntry](t, s, "journal_get", map[string]any{"id": j.ID}); len(got.Attachments) != 1 {
		t.Fatalf("ready attachment listed: %+v", got.Attachments)
	}
	// Wrong size or type is refused before anything is stored.
	b := execute[engine.Attachment](t, s, "attachments_upload_init", map[string]any{"idempotency_key": key(), "owner_type": "journal_entry", "owner_id": j.ID, "filename": "x.png", "content_type": "image/png", "size_bytes": 8})
	if _, err = s.Upload(t.Context(), b.ID, "image/png", strings.NewReader("short")); engine.PublicError(err).Code != "invalid_input" {
		t.Fatalf("size mismatch: %v", err)
	}
	if _, err = s.Upload(t.Context(), b.ID, "image/jpeg", strings.NewReader("12345678")); engine.PublicError(err).Code != "invalid_input" {
		t.Fatalf("type mismatch: %v", err)
	}
	if _, _, err = s.Download(t.Context(), b.ID); engine.PublicError(err).Code != "not_found" {
		t.Fatalf("pending download: %v", err)
	}
	// Removing hides it; restoring brings it back.
	del := execute[engine.Attachment](t, s, "attachments_delete", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 2})
	if _, _, err = s.Download(t.Context(), a.ID); engine.PublicError(err).Code != "not_found" || del.DeletedAt == nil {
		t.Fatalf("deleted download: %v", err)
	}
	execute[engine.Attachment](t, s, "attachments_restore", map[string]any{"idempotency_key": key(), "id": a.ID, "expected_version": 3})
	if _, _, err = s.Download(t.Context(), a.ID); err != nil {
		t.Fatalf("restored download: %v", err)
	}
}

func TestAttachmentsNeedStorage(t *testing.T) {
	_, pool := integrationEnv(t)
	s := engine.New(pool, nil)
	j := execute[engine.JournalEntry](t, s, "journal_create", map[string]any{"idempotency_key": key(), "text": "x"})
	errorCode(t, s, "attachments_upload_init", map[string]any{"idempotency_key": key(), "owner_type": "journal_entry", "owner_id": j.ID, "filename": "a.png", "content_type": "image/png", "size_bytes": 1}, "busy")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
