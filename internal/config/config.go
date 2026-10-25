// Package config loads TaktaPlus settings from environment variables.
package config

import (
	"errors"
	"os"
	"time"
)

// Config holds runtime settings.
type Config struct {
	Listen          string        // TAKTAPLUS_LISTEN, default ":8080" (TLS arrives in phase 1)
	DatabaseURL     string        // TAKTAPLUS_DATABASE_URL, required
	DataDir         string        // TAKTAPLUS_DATA_DIR, default /var/lib/taktaplus
	HostRoot        string        // TAKTAPLUS_HOST_ROOT: where host identifiers are mounted, default "/"
	ShutdownTimeout time.Duration // TAKTAPLUS_SHUTDOWN_TIMEOUT, default 15s
	LogLevel        string        // TAKTAPLUS_LOG_LEVEL: debug, info, warn, error
}

// FromEnv reads the configuration from the process environment.
func FromEnv() (Config, error) {
	c := Config{
		Listen:          getenv("TAKTAPLUS_LISTEN", ":8080"),
		DatabaseURL:     os.Getenv("TAKTAPLUS_DATABASE_URL"),
		DataDir:         getenv("TAKTAPLUS_DATA_DIR", "/var/lib/taktaplus"),
		HostRoot:        getenv("TAKTAPLUS_HOST_ROOT", "/"),
		ShutdownTimeout: 15 * time.Second,
		LogLevel:        getenv("TAKTAPLUS_LOG_LEVEL", "info"),
	}
	if v := os.Getenv("TAKTAPLUS_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, errors.New("config: TAKTAPLUS_SHUTDOWN_TIMEOUT must be a duration such as 15s")
		}
		c.ShutdownTimeout = d
	}
	if c.DatabaseURL == "" {
		return c, errors.New("config: TAKTAPLUS_DATABASE_URL is required")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
