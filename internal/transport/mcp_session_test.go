package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/realnighthawk/life-engine/internal/blobs"
	"github.com/realnighthawk/life-engine/internal/engine"
	"github.com/realnighthawk/life-engine/internal/store/postgres"
)

// The hub forwards the calling agent session as MCP _meta; what the call creates must remember it.
func TestMCPCallRecordsTheCallingSession(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	service := engine.New(pool, blobs.NewMemory())
	handler, err := New(service, http.NewServeMux(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	call := func(name string, args map[string]any, meta map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args, Meta: meta})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		var out map[string]any
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
		return out
	}
	key := "agent:main:web:user:meta-test:session:s1"
	created := call("reminders_create", map[string]any{"idempotency_key": "meta-" + time.Now().Format("150405.000000"), "title": "Via MCP"},
		map[string]any{engine.SessionMetaKey: key})
	got := call("reminders_get", map[string]any{"id": created["id"]}, nil)
	if got["session_id"] != key {
		t.Fatalf("session_id = %v, want %s", got["session_id"], key)
	}
}
