package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr           string
	DatabasePath   string
	AdminUsername  string
	AdminPassword  string
	SessionSecret  string
	SecureCookies  bool
	PublicBaseURL  string
	StatsTTL       time.Duration
	RequestTimeout time.Duration
}

func Load() (Config, error) {
	publicBaseURL := os.Getenv("PUBLIC_BASE_URL")
	cfg := Config{
		Addr:           envOrDefault("APP_ADDR", ":8099"),
		DatabasePath:   envOrDefault("DATABASE_PATH", "./data/redirector.db"),
		AdminUsername:  envOrDefault("ADMIN_USERNAME", "admin"),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
		SessionSecret:  envOrDefault("SESSION_SECRET", "change-me"),
		SecureCookies:  envBoolOrDefault("SESSION_SECURE_COOKIE", strings.HasPrefix(strings.ToLower(publicBaseURL), "https://")),
		PublicBaseURL:  publicBaseURL,
		StatsTTL:       time.Duration(envIntOrDefault("STATS_TTL_SECONDS", 10)) * time.Second,
		RequestTimeout: time.Duration(envIntOrDefault("REQUEST_TIMEOUT_SECONDS", 15)) * time.Second,
	}

	if cfg.AdminPassword == "" {
		return Config{}, fmt.Errorf("ADMIN_PASSWORD is required")
	}
	if len(cfg.AdminPassword) < 12 {
		return Config{}, fmt.Errorf("ADMIN_PASSWORD must be at least 12 characters")
	}
	if len(cfg.SessionSecret) < 32 {
		return Config{}, fmt.Errorf("SESSION_SECRET must be at least 32 characters")
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}
