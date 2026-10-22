package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/realnighthawk/life-engine/internal/engine"
)

func New(service *engine.Service, health http.Handler, trustedOrigins []string) (http.Handler, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "life-engine", Version: "0.1.0"}, nil)
	closed := false
	for _, op := range service.Operations() {
		server.AddTool(&mcp.Tool{
			Name: op.Name, Description: op.Description, InputSchema: op.InputSchema, OutputSchema: op.OutputSchema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !op.Write, IdempotentHint: op.Write, OpenWorldHint: &closed},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if key, ok := req.Params.GetMeta()[engine.SessionMetaKey].(string); ok {
				ctx = engine.WithSession(ctx, key) // the hub passes on which agent session is calling
			}
			data, err := service.Execute(ctx, op.Name, req.Params.Arguments)
			if err != nil {
				public := engine.PublicError(err)
				if public.Code == "internal" {
					slog.Error("life operation failed", "operation", op.Name)
				}
				body, _ := json.Marshal(map[string]any{"error": public})
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil
			}
			return &mcp.CallToolResult{StructuredContent: json.RawMessage(data), Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
		})
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	api := http.NewServeMux()
	api.Handle("/mcp", mcpHandler)
	api.HandleFunc("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"operations": service.Operations()})
	})
	api.HandleFunc("GET /api/v1/openapi.json", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, service.OpenAPI()) })
	api.HandleFunc("POST /api/v1/operations/{operation}", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
			writeError(w, &engine.Error{Code: "invalid_input", Message: "Content-Type must be application/json"})
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, 413, map[string]any{"error": &engine.Error{Code: "too_large", Message: "Request body exceeds 1 MiB"}})
			return
		}
		result, err := service.Execute(r.Context(), r.PathValue("operation"), raw)
		if err != nil {
			public := engine.PublicError(err)
			if public.Code == "internal" {
				slog.Error("life operation failed", "operation", r.PathValue("operation"))
			}
			writeError(w, public)
			return
		}
		writeJSON(w, 200, json.RawMessage(result))
	})
	bounded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		api.ServeHTTP(w, r)
	})
	originGuard, err := newOriginGuard(trustedOrigins)
	if err != nil {
		return nil, err
	}
	root := http.NewServeMux()
	root.Handle("/mcp", originGuard(bounded))
	root.Handle("/api/v1/operations/", originGuard(bounded))
	root.Handle("/api/v1/capabilities", bounded)
	root.Handle("/api/v1/openapi.json", bounded)
	root.Handle("/", health)
	return root, nil
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func writeError(w http.ResponseWriter, err *engine.Error) {
	status := 500
	switch err.Code {
	case "invalid_input":
		status = 400
	case "not_found":
		status = 404
	case "too_large":
		status = 413
	case "busy":
		status = 503
	case "conflict", "duplicate":
		status = 409
	}
	writeJSON(w, status, map[string]any{"error": err})
}
