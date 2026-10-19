package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/realnighthawk/life-engine/internal/blobs"
	"github.com/realnighthawk/life-engine/internal/engine"
)

// Discovery is the harness integration: the exact typed operations appear in both catalogs.
func TestTrackerToolsDiscoveredOverMCPAndHTTP(t *testing.T) {
	service := engine.New(nil, blobs.NewMemory())
	handler, err := New(service, http.NewServeMux(), nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "tracker-client", Version: "test"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/capabilities", nil))
	var capabilities struct {
		Operations []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"operations"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	httpTools := map[string]json.RawMessage{}
	for _, op := range capabilities.Operations {
		httpTools[op.Name] = op.InputSchema
	}
	for _, name := range []string{"tracker_catalog_get", "views_state", "goal_measurements_record", "goal_measurements_update", "goal_measurements_delete"} {
		tool := tools[name]
		if tool == nil || tool.InputSchema == nil || len(httpTools[name]) == 0 {
			t.Fatalf("missing typed tool %s", name)
		}
	}
}
