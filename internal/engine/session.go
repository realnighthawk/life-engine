package engine

import (
	"context"
	"strings"
)

type sessionContextKey struct{}

// WithSession records which agent session a call came from, so what it creates can wake that session later. The MCP
// transport sets it from the call's _meta; the HTTP API has no session.
func WithSession(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, strings.TrimSpace(key))
}

func sessionFrom(ctx context.Context) string {
	s, _ := ctx.Value(sessionContextKey{}).(string)
	return s
}

// SessionMetaKey is the MCP _meta field carrying the calling session key.
const SessionMetaKey = "nighthawk/session_key"

// sessionScope is the user-stable session a wake wakes: the key without a per-branch ":session:<id>" or per-thread
// ":thread:<id>" suffix, as the harness's ids.user_scope_of computes it.
func sessionScope(key string) string {
	for _, marker := range []string{":session:", ":thread:"} {
		if head, _, found := strings.Cut(key, marker); found {
			return head
		}
	}
	return key
}
