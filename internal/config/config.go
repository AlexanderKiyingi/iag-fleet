package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/corsenv"
)

// Config holds runtime settings for the fleet API.
type Config struct {
	Addr                  string
	Environment           string
	DatabaseURL           string
	TelemetryDatabaseURL  string
	JWTIssuer             string
	JWKSURL               string
	Audience              string // aud claim the service requires on inbound tokens
	GatewayAPIPrefix      string
	CORSOrigin            string
	PublicAPIURL          string
	AutoMigrate           bool
	KafkaBrokers          []string
	EventBusEnabled       bool
	ServiceClientID       string
	ServiceClientSecret   string
	AuthTokenURL          string
}

// Load reads configuration from env. Hard cutover: no AUTH_MODE, no
// GATEWAY_INTERNAL_SECRET — every inbound request must carry a verifiable
// Bearer token with aud=iag.fleet.
func Load() (Config, error) {
	env := strings.ToLower(strings.TrimSpace(envOr("ENVIRONMENT", envOr("APP_ENV", "development"))))
	issuer := envOr("JWT_ISSUER", "http://localhost:3001")
	cfg := Config{
		Addr:                 ListenAddr(),
		Environment:          env,
		DatabaseURL:          strings.TrimSpace(os.Getenv("DATABASE_URL")),
		TelemetryDatabaseURL: strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL")),
		JWTIssuer:            issuer,
		JWKSURL:              envOr("JWKS_URL", strings.TrimRight(issuer, "/")+"/.well-known/jwks.json"),
		Audience:             envOr("AUDIENCE", "iag.fleet"),
		GatewayAPIPrefix:     strings.TrimSpace(envOr("GATEWAY_API_PREFIX", "/api/v1/fleet")),
		CORSOrigin:           corsenv.Allowlist(corsenv.DefaultDevOrigins),
		PublicAPIURL:         strings.TrimRight(strings.TrimSpace(envOr("PUBLIC_API_URL", "")), "/"),
		AutoMigrate:          envOr("AUTO_MIGRATE", "true") != "false",
		EventBusEnabled:      strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
		ServiceClientID:      strings.TrimSpace(envOr("SERVICE_CLIENT_ID", "iag-fleet")),
		ServiceClientSecret:  strings.TrimSpace(os.Getenv("SERVICE_CLIENT_SECRET")),
		AuthTokenURL:         strings.TrimSpace(envOr("AUTH_TOKEN_URL", strings.TrimRight(issuer, "/")+"/oauth/token")),
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
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.IsProduction() {
		if c.HasWildcardCORS() {
			return fmt.Errorf("set ALLOWED_ORIGINS in production (not *)")
		}
		if c.ServiceClientSecret == "" {
			return fmt.Errorf("SERVICE_CLIENT_SECRET is required in production")
		}
		if len(c.ServiceClientSecret) < 16 {
			return fmt.Errorf("SERVICE_CLIENT_SECRET must be at least 16 characters in production")
		}
		if c.AutoMigrate {
			return fmt.Errorf("AUTO_MIGRATE must be false in production (run migrations out of band)")
		}
	}
	return nil
}

func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "prod"
}

func (c Config) HasWildcardCORS() bool {
	for _, o := range strings.Split(c.CORSOrigin, ",") {
		if strings.TrimSpace(o) == "*" {
			return true
		}
	}
	return c.CORSOrigin == "*"
}

func (c Config) TelemetrySplit() bool {
	return c.TelemetryDatabaseURL != "" && c.TelemetryDatabaseURL != c.DatabaseURL
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
