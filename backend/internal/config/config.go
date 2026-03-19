package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	ListenAddr      string
	StoragePath     string
	DBPath          string
	MaxFileSize     int64
	DefaultExpiry   time.Duration
	CleanupInterval time.Duration
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	return Config{
		ListenAddr:      envOrDefault("LISTEN_ADDR", ":8080"),
		StoragePath:     envOrDefault("STORAGE_PATH", "./data/files"),
		DBPath:          envOrDefault("DB_PATH", "./data/psst.db"),
		MaxFileSize:     envOrDefaultInt64("MAX_FILE_SIZE", 5*1024*1024*1024), // 5 GB
		DefaultExpiry:   envOrDefaultDuration("DEFAULT_EXPIRY", 24*time.Hour),
		CleanupInterval: envOrDefaultDuration("CLEANUP_INTERVAL", 5*time.Minute),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envOrDefaultDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
