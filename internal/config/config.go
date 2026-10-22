package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL, ListenAddr string
	TrustedOrigins          []string
	// Alerts: both the address and the tenant, or neither. Namespace and the coordinator's task queue default to the harness's.
	TemporalAddress, TemporalNamespace, TenantSlug string
	// CoordinatorTaskQueue is where the harness's session coordinators run; EngineTaskQueue is this engine's own.
	CoordinatorTaskQueue, EngineTaskQueue string
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), ListenAddr: os.Getenv("LISTEN_ADDR"), TrustedOrigins: strings.Split(os.Getenv("TRUSTED_ORIGINS"), ",")}
	c.TemporalAddress, c.TemporalNamespace, c.TenantSlug = os.Getenv("TEMPORAL_ADDRESS"), os.Getenv("TEMPORAL_NAMESPACE"), os.Getenv("TENANT_SLUG")
	c.CoordinatorTaskQueue, c.EngineTaskQueue = os.Getenv("TEMPORAL_WORKFLOW_TASK_QUEUE"), os.Getenv("LIFE_ENGINE_TASK_QUEUE")
	if c.TemporalNamespace == "" {
		c.TemporalNamespace = "agents"
	}
	if c.CoordinatorTaskQueue == "" {
		c.CoordinatorTaskQueue = "agent-loop"
	}
	if c.EngineTaskQueue == "" {
		c.EngineTaskQueue = "life-engine"
	}
	if (c.TemporalAddress == "") != (c.TenantSlug == "") {
		return c, errors.New("configure TEMPORAL_ADDRESS and TENANT_SLUG together or leave both unset")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8093"
	}
	for i := range c.TrustedOrigins {
		c.TrustedOrigins[i] = strings.TrimSpace(c.TrustedOrigins[i])
	}
	return c, nil
}
