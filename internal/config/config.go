package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL, ListenAddr                                           string
	TrustedOrigins                                                    []string
	MinioEndpoint, MinioBucket, MinioAccess, MinioSecret, MinioRegion string
	// Wakes: all of address, tenant and session key, or none. Namespace and task queue default to the harness's.
	TemporalAddress, TemporalNamespace, TemporalTaskQueue, TenantSlug, WakeSessionKey string
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), ListenAddr: os.Getenv("LISTEN_ADDR"), TrustedOrigins: strings.Split(os.Getenv("TRUSTED_ORIGINS"), ","),
		MinioEndpoint: os.Getenv("MINIO_ENDPOINT"), MinioBucket: os.Getenv("MINIO_BUCKET"), MinioAccess: os.Getenv("MINIO_ACCESS_KEY"), MinioSecret: os.Getenv("MINIO_SECRET_KEY"), MinioRegion: os.Getenv("MINIO_REGION")}
	c.TemporalAddress, c.TemporalNamespace, c.TemporalTaskQueue = os.Getenv("TEMPORAL_ADDRESS"), os.Getenv("TEMPORAL_NAMESPACE"), os.Getenv("TEMPORAL_WORKFLOW_TASK_QUEUE")
	c.TenantSlug, c.WakeSessionKey = os.Getenv("TENANT_SLUG"), os.Getenv("LIFE_WAKE_SESSION_KEY")
	if c.TemporalNamespace == "" {
		c.TemporalNamespace = "agents"
	}
	if c.TemporalTaskQueue == "" {
		c.TemporalTaskQueue = "agent-loop"
	}
	if (c.TemporalAddress == "") != (c.TenantSlug == "") || (c.TenantSlug == "") != (c.WakeSessionKey == "") {
		return c, errors.New("configure TEMPORAL_ADDRESS, TENANT_SLUG and LIFE_WAKE_SESSION_KEY together or leave all unset")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8093"
	}
	count := 0
	for _, v := range []string{c.MinioEndpoint, c.MinioBucket, c.MinioAccess, c.MinioSecret} {
		if v != "" {
			count++
		}
	}
	if count != 0 && count != 4 {
		return c, errors.New("configure all MinIO endpoint, bucket and credential values or leave all unset")
	}
	if c.MinioRegion == "" {
		c.MinioRegion = "us-east-1"
	}
	for i := range c.TrustedOrigins {
		c.TrustedOrigins[i] = strings.TrimSpace(c.TrustedOrigins[i])
	}
	return c, nil
}
