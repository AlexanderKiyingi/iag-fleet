package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds runtime settings for the fleet API.
type Config struct {
	Addr             string
	AuthMode         string
	GatewaySecret    string
	JWTIssuer        string
	JWKSURL          string
	GatewayAPIPrefix string
	CORSOrigin       string
	AppName          string
	AppURL           string
	PublicAPIURL     string
	AutoMigrate      bool
	KafkaBrokers     []string
	EventBusEnabled  bool
}

// Load reads configuration from the environment.
func Load() (Config, error) {
	authMode := strings.ToLower(strings.TrimSpace(envOr("AUTH_MODE", "gateway")))
	switch authMode {
	case "gateway", "jwt":
	default:
		return Config{}, fmt.Errorf("AUTH_MODE must be gateway or jwt (got %q)", authMode)
	}

	cfg := Config{
		Addr:             envOr("ADDR", envOr("HTTP_PORT", ":4008")),
		AuthMode:         authMode,
		GatewaySecret:    strings.TrimSpace(os.Getenv("GATEWAY_INTERNAL_SECRET")),
		JWTIssuer:        envOr("JWT_ISSUER", "http://localhost:3001"),
		JWKSURL:          envOr("JWKS_URL", "http://127.0.0.1:3001/.well-known/jwks.json"),
		GatewayAPIPrefix: strings.TrimSpace(envOr("GATEWAY_API_PREFIX", "/api/v1/fleet")),
		CORSOrigin:       envOr("CORS_ORIGIN", "http://localhost:3000"),
		AppName:          envOr("APP_NAME", "IAG Fleet"),
		AppURL:           envOr("APP_URL", "http://localhost:3000"),
		PublicAPIURL:     strings.TrimRight(strings.TrimSpace(envOr("PUBLIC_API_URL", "")), "/"),
		AutoMigrate:      envOr("AUTO_MIGRATE", "true") != "false",
		EventBusEnabled:  strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
	}
	if brokers := strings.TrimSpace(os.Getenv("KAFKA_BROKERS")); brokers != "" {
		for _, b := range strings.Split(brokers, ",") {
			if t := strings.TrimSpace(b); t != "" {
				cfg.KafkaBrokers = append(cfg.KafkaBrokers, t)
			}
		}
	}
	if cfg.EventBusEnabled && len(cfg.KafkaBrokers) == 0 {
		cfg.KafkaBrokers = []string{"127.0.0.1:19092"}
	}

	if !strings.HasPrefix(cfg.Addr, ":") && !strings.Contains(cfg.Addr, ":") {
		cfg.Addr = ":" + cfg.Addr
	}

	if cfg.AuthMode == "gateway" && cfg.GatewaySecret == "" {
		return Config{}, fmt.Errorf("AUTH_MODE=gateway requires GATEWAY_INTERNAL_SECRET")
	}
	if cfg.AuthMode == "gateway" && len(cfg.GatewaySecret) < 16 {
		return Config{}, fmt.Errorf("GATEWAY_INTERNAL_SECRET must be at least 16 characters")
	}
	if cfg.AuthMode == "jwt" && cfg.JWKSURL == "" {
		return Config{}, fmt.Errorf("AUTH_MODE=jwt requires JWKS_URL")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
