package config

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL, ListenAddr string
	TrustedOrigins          []string
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), ListenAddr: os.Getenv("LISTEN_ADDR"), TrustedOrigins: strings.Split(os.Getenv("TRUSTED_ORIGINS"), ",")}
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
