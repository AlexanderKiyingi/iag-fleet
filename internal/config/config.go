package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds runtime settings for the fleet API.
type Config struct {
	Addr             string
	JWTIssuer        string
	JWKSURL          string
	Audience         string // aud claim the service requires on inbound tokens
	GatewayAPIPrefix string
	CORSOrigin       string
	PublicAPIURL     string
	AutoMigrate      bool
	KafkaBrokers     []string
	EventBusEnabled  bool

	ServiceClientID     string
	ServiceClientSecret string
	AuthTokenURL        string
}

// Load reads configuration from env. Hard cutover: no AUTH_MODE, no
// GATEWAY_INTERNAL_SECRET — every inbound request must carry a verifiable
// Bearer token with aud=iag.fleet.
func Load() (Config, error) {
	issuer := envOr("JWT_ISSUER", "http://localhost:3001")
	cfg := Config{
		Addr:             ListenAddr(),
		JWTIssuer:        issuer,
		JWKSURL:          envOr("JWKS_URL", strings.TrimRight(issuer, "/")+"/.well-known/jwks.json"),
		Audience:         envOr("AUDIENCE", "iag.fleet"),
		GatewayAPIPrefix: strings.TrimSpace(envOr("GATEWAY_API_PREFIX", "/api/v1/fleet")),
		CORSOrigin:       envOr("CORS_ORIGIN", "http://localhost:3000"),
		PublicAPIURL:     strings.TrimRight(strings.TrimSpace(envOr("PUBLIC_API_URL", "")), "/"),
		AutoMigrate:      envOr("AUTO_MIGRATE", "true") != "false",
		EventBusEnabled:     strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
		ServiceClientID:     strings.TrimSpace(envOr("SERVICE_CLIENT_ID", "iag-fleet")),
		ServiceClientSecret: os.Getenv("SERVICE_CLIENT_SECRET"),
		AuthTokenURL:        strings.TrimSpace(envOr("AUTH_TOKEN_URL", strings.TrimRight(issuer, "/")+"/oauth/token")),
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

	if cfg.Audience == "" {
		return Config{}, fmt.Errorf("AUDIENCE is required (e.g. iag.fleet)")
	}
	if cfg.JWKSURL == "" {
		return Config{}, fmt.Errorf("JWKS_URL is required")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
