package app

import (
	"fmt"
	"os"
	"path/filepath"
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

func envOptionalStringWithFile(key string) (string, error) {
	value, valueSet := os.LookupEnv(key)
	fileValue, fileSet := os.LookupEnv(key + "_FILE")
	if valueSet && strings.TrimSpace(value) != "" && fileSet && strings.TrimSpace(fileValue) != "" {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", key, key)
	}
	if fileSet && strings.TrimSpace(fileValue) != "" {
		path := strings.TrimSpace(fileValue)
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s_FILE %s: %w", key, filepath.Clean(path), err)
		}
		return strings.TrimSpace(string(content)), nil
	}
	if valueSet {
		return strings.TrimSpace(value), nil
	}
	return "", nil
}

func envStringWithFile(key, fallback string) (string, error) {
	value, err := envOptionalStringWithFile(key)
	if err != nil {
		return "", err
	}
	if value != "" {
		return value, nil
	}
	return fallback, nil
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
	bootstrapSyncOnStartup, err := envBool("BOOTSTRAP_SYNC_ON_STARTUP", false)
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
	loginRateLimitWindowSeconds, err := envInt("LOGIN_RATE_LIMIT_WINDOW_SECONDS", 600)
	if err != nil {
		return Config{}, err
	}
	loginRateLimitMaxAttempts, err := envInt("LOGIN_RATE_LIMIT_MAX_ATTEMPTS", 10)
	if err != nil {
		return Config{}, err
	}
	alertEvalSeconds, err := envInt("ALERT_EVAL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	alertBackupMaxAgeMinutes, err := envInt("ALERT_BACKUP_MAX_AGE_MINUTES", 1560)
	if err != nil {
		return Config{}, err
	}
	alertBacklogMaxJobs, err := envInt("ALERT_BACKLOG_MAX_JOBS", 25)
	if err != nil {
		return Config{}, err
	}
	alertBacklogMaxAgeMinutes, err := envInt("ALERT_BACKLOG_MAX_AGE_MINUTES", 60)
	if err != nil {
		return Config{}, err
	}
	alertLoginThrottleThreshold, err := envInt("ALERT_LOGIN_THROTTLE_THRESHOLD", 3)
	if err != nil {
		return Config{}, err
	}
	alertExtractorFailureThreshold, err := envInt("ALERT_EXTRACTOR_FAILURE_THRESHOLD", 5)
	if err != nil {
		return Config{}, err
	}
	secretKey, err := envStringWithFile("SECRET_KEY", "change-me-now")
	if err != nil {
		return Config{}, err
	}
	bootstrapAdminPassword, err := envOptionalStringWithFile("BOOTSTRAP_ADMIN_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	bootstrapTenantName := envString("BOOTSTRAP_TENANT_NAME", "KolaboSolutions")
	bootstrapTenantSlug := envOptionalString("BOOTSTRAP_TENANT_SLUG")
	if strings.TrimSpace(bootstrapTenantSlug) == "" {
		bootstrapTenantSlug = normalizeTenantSlug("", bootstrapTenantName)
	}
	return Config{
		AppEnv:                         appEnv,
		AppAddr:                        envString("APP_ADDR", ":8080"),
		DataPath:                       envString("DATA_PATH", "./data/store.db"),
		BackupDir:                      envString("BACKUP_DIR", "./backups"),
		SecretKey:                      secretKey,
		ExtractorURL:                   envString("EXTRACTOR_URL", "http://extractor:9090"),
		TreasuryFeedURL:                envOptionalString("TREASURY_FEED_URL"),
		AlertWebhookURL:                envOptionalString("ALERT_WEBHOOK_URL"),
		SecureCookies:                  secureCookies,
		LowMemoryMode:                  lowMemoryMode,
		AnalyticsEnabled:               analyticsEnabled,
		BootstrapSyncOnStartup:         bootstrapSyncOnStartup,
		SessionHours:                   sessionHours,
		WorkerSyncMinutes:              workerSyncMinutes,
		WorkerLoopSeconds:              workerLoopSeconds,
		LoginRateLimitWindowSeconds:    loginRateLimitWindowSeconds,
		LoginRateLimitMaxAttempts:      loginRateLimitMaxAttempts,
		AlertEvalSeconds:               alertEvalSeconds,
		AlertBackupMaxAgeMinutes:       alertBackupMaxAgeMinutes,
		AlertBacklogMaxJobs:            alertBacklogMaxJobs,
		AlertBacklogMaxAgeMinutes:      alertBacklogMaxAgeMinutes,
		AlertLoginThrottleThreshold:    alertLoginThrottleThreshold,
		AlertExtractorFailureThreshold: alertExtractorFailureThreshold,
		BootstrapAdminUsername:         envString("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminEmail:            envString("BOOTSTRAP_ADMIN_EMAIL", "admin@localhost"),
		BootstrapAdminPassword:         bootstrapAdminPassword,
		BootstrapTenantName:            bootstrapTenantName,
		BootstrapTenantSlug:            bootstrapTenantSlug,
	}, nil
}
