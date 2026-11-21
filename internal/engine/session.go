package engine

import (
	"context"
	"regexp"
	"strings"
)

type sessionContextKey struct{}
type mcpAgentContextKey struct{}

// Match the agent-harness agent_id constraint (076_agent_name_ids.sql).
var agentIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$`)

// WithMCPAgent records provenance from a direct MCP client's runtime configuration, not authentication.
// It does not imply a session and cannot be used to route wakes.
func WithMCPAgent(ctx context.Context, agentID string) context.Context {
	if !agentIDPattern.MatchString(agentID) {
		return ctx
	}
	return context.WithValue(ctx, mcpAgentContextKey{}, agentID)
}

func artifactCreator(ctx context.Context) string {
	if rest, ok := strings.CutPrefix(sessionFrom(ctx), "agent:"); ok {
		if agent, suffix, ok := strings.Cut(rest, ":"); ok && suffix != "" && agentIDPattern.MatchString(agent) {
			return agent
		}
	}
	agent, _ := ctx.Value(mcpAgentContextKey{}).(string)
	return agent
}

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
