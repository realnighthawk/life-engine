package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func TestHealthAndCapabilities(t *testing.T) {
	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	h, err := New(engine.New(nil, nil), health, nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/healthz": 200, "/api/v1/capabilities": 200, "/api/v1/openapi.json": 200} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/operations/nope", nil)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("unknown operation: %d", rec.Code)
	}
}
