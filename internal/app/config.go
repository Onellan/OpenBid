package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func envString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func envOptionalString(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func envBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be one of true/false, yes/no, on/off, or 1/0", key)
	}
}

func envInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number: %w", key, err)
	}
	return parsed, nil
}

func loadConfigFromEnv() (Config, error) {
	appEnv := normalizeAppEnv(envString("APP_ENV", "development"))
	secureCookies, err := envBool("SECURE_COOKIES", false)
	if err != nil {
		return Config{}, err
	}
	lowMemoryMode, err := envBool("LOW_MEMORY_MODE", true)
	if err != nil {
		return Config{}, err
	}
	analyticsEnabled, err := envBool("ANALYTICS_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	bootstrapSyncOnStartup, err := envBool("BOOTSTRAP_SYNC_ON_STARTUP", appEnv != "production")
	if err != nil {
		return Config{}, err
	}
	sessionHours, err := envInt("SESSION_HOURS", 12)
	if err != nil {
		return Config{}, err
	}
	workerSyncMinutes, err := envInt("WORKER_SYNC_MINUTES", 360)
	if err != nil {
		return Config{}, err
	}
	workerLoopSeconds, err := envInt("WORKER_LOOP_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	return Config{
		AppEnv:                 appEnv,
		AppAddr:                envString("APP_ADDR", ":8080"),
		DataPath:               envString("DATA_PATH", "./data/store.db"),
		SecretKey:              envString("SECRET_KEY", "change-me-now"),
		ExtractorURL:           envString("EXTRACTOR_URL", "http://extractor:9090"),
		TreasuryFeedURL:        envOptionalString("TREASURY_FEED_URL"),
		SecureCookies:          secureCookies,
		LowMemoryMode:          lowMemoryMode,
		AnalyticsEnabled:       analyticsEnabled,
		BootstrapSyncOnStartup: bootstrapSyncOnStartup,
		SessionHours:           sessionHours,
		WorkerSyncMinutes:      workerSyncMinutes,
		WorkerLoopSeconds:      workerLoopSeconds,
		BootstrapAdminUsername: envString("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminEmail:    envString("BOOTSTRAP_ADMIN_EMAIL", "admin@localhost"),
		BootstrapAdminPassword: envOptionalString("BOOTSTRAP_ADMIN_PASSWORD"),
	}, nil
}
