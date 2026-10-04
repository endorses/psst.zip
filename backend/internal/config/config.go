package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	MaxActiveStreams      int
	MaxStreamsPerAccount  int
	MaxStreamsPerIP       int
	MaxStreamsPerTransfer int
	MaxStreamsPerSlot     int
	MaxActiveRequests     int
	MaxRecoveryRequests   int
	AdminUsername         string
	AdminPassword         string
	PublicURL             string
	AuthAllowInsecureHTTP bool
	MaxSlotTransfers      int
	MaxSlotSize           int64
	MaxSlotExpiry         time.Duration
	ListenAddr            string
	StoragePath           string
	DBPath                string
	MaxFileSize           int64
	DefaultExpiry         time.Duration
	CleanupInterval       time.Duration

	AllowLegacyDeletion bool // ID-only deletion for pre-token links; disabled by default.

	// Security settings
	TrustedProxies         string  // Explicit comma-separated CIDRs; default trusts no forwarded headers.
	CORSOrigin             string  // Allowed CORS origin (default "*")
	RateLimitGlobal        float64 // Global requests per second per IP (default 20)
	RateLimitCreation      float64 // Creation endpoint requests per second per IP (default 2)
	RateLimitBurst         int     // Global burst size (default 40)
	RateLimitCreationBurst int     // Creation burst size (default 5)
	MaxManifestSize        int64   // Max manifest upload size in bytes (default and maximum 1 MiB)
	MaxFilesPerTransfer    int     // Max number of files per transfer (default and protocol ceiling 100)
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	return Config{
		MaxActiveStreams:      int(envOrDefaultInt64("MAX_ACTIVE_STREAMS", 64)),
		MaxStreamsPerAccount:  int(envOrDefaultInt64("MAX_STREAMS_PER_ACCOUNT", 4)),
		MaxStreamsPerIP:       int(envOrDefaultInt64("MAX_STREAMS_PER_IP", 4)),
		MaxStreamsPerTransfer: int(envOrDefaultInt64("MAX_STREAMS_PER_TRANSFER", 4)),
		MaxStreamsPerSlot:     int(envOrDefaultInt64("MAX_STREAMS_PER_SLOT", 4)),
		MaxActiveRequests:     int(envOrDefaultInt64("MAX_ACTIVE_REQUESTS", 128)),
		MaxRecoveryRequests:   int(envOrDefaultInt64("MAX_RECOVERY_REQUESTS", 32)),
		AdminUsername:         os.Getenv("ADMIN_USERNAME"),
		AdminPassword:         os.Getenv("ADMIN_PASSWORD"),
		PublicURL:             os.Getenv("PUBLIC_URL"),
		AuthAllowInsecureHTTP: envOrDefaultBool("AUTH_ALLOW_INSECURE_HTTP", false),
		MaxSlotTransfers:      int(envOrDefaultInt64("MAX_SLOT_TRANSFERS", 20)),
		MaxSlotExpiry:         envOrDefaultDuration("MAX_SLOT_EXPIRY", 168*time.Hour),
		MaxSlotSize:           envOrDefaultInt64("MAX_SLOT_SIZE", 5*1024*1024*1024),
		ListenAddr:            envOrDefault("LISTEN_ADDR", ":8080"),
		StoragePath:           envOrDefault("STORAGE_PATH", "./data/files"),
		DBPath:                envOrDefault("DB_PATH", "./data/psst.db"),
		MaxFileSize:           envOrDefaultInt64("MAX_FILE_SIZE", 5*1024*1024*1024), // 5 GB
		DefaultExpiry:         envOrDefaultDuration("DEFAULT_EXPIRY", 24*time.Hour),
		CleanupInterval:       envOrDefaultDuration("CLEANUP_INTERVAL", 5*time.Minute),

		AllowLegacyDeletion:    envOrDefaultBool("ALLOW_LEGACY_DELETION", false),
		TrustedProxies:         os.Getenv("TRUSTED_PROXIES"),
		CORSOrigin:             envOrDefault("CORS_ORIGIN", "*"),
		RateLimitGlobal:        envOrDefaultFloat64("RATE_LIMIT_GLOBAL", 20),
		RateLimitCreation:      envOrDefaultFloat64("RATE_LIMIT_CREATION", 2),
		RateLimitBurst:         int(envOrDefaultInt64("RATE_LIMIT_BURST", 40)),
		RateLimitCreationBurst: int(envOrDefaultInt64("RATE_LIMIT_CREATION_BURST", 5)),
		MaxManifestSize:        envOrDefaultInt64("MAX_MANIFEST_SIZE", 1024*1024), // 1 MiB; uploads also enforce this ceiling
		MaxFilesPerTransfer:    int(envOrDefaultInt64("MAX_FILES_PER_TRANSFER", 100)),
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

func envOrDefaultFloat64(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
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

func envOrDefaultBool(key string, fallback bool) bool {
	value, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}
