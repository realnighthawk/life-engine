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
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), ListenAddr: os.Getenv("LISTEN_ADDR"), TrustedOrigins: strings.Split(os.Getenv("TRUSTED_ORIGINS"), ","),
		MinioEndpoint: os.Getenv("MINIO_ENDPOINT"), MinioBucket: os.Getenv("MINIO_BUCKET"), MinioAccess: os.Getenv("MINIO_ACCESS_KEY"), MinioSecret: os.Getenv("MINIO_SECRET_KEY"), MinioRegion: os.Getenv("MINIO_REGION")}
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
