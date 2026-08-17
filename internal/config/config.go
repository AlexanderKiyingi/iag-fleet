package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/corsenv"
)

// Config holds runtime settings for the fleet API.
type Config struct {
	Addr                 string
	Environment          string
	DatabaseURL          string
	TelemetryDatabaseURL string
	JWTIssuer            string
	JWKSURL              string
	Audience             string // aud claim the service requires on inbound tokens
	GatewayAPIPrefix     string
	CORSOrigin           string
	PublicAPIURL         string
	AutoMigrate          bool
	KafkaBrokers         []string
	// TrustedProxies pins gin's trusted proxies (gateway/edge CIDR) so the
	// per-IP rate limiter keys on the real, non-spoofable client IP. Empty leaves
	// gin's default (trust all) — spoofable; set behind the gateway.
	TrustedProxies []string
	EventBusEnabled      bool
	ServiceClientID      string
	ServiceClientSecret  string
	AuthTokenURL         string

	// Warehouse ("stores") delegation. When WarehouseDelegationEnabled is
	// true, fleet stops decrementing its local parts.stock on maintenance WO
	// completion and instead posts a stock issue to iag-warehouse, the
	// system-of-record for spare-parts stock. The other fields configure how
	// fleet reaches and labels those issues.
	WarehouseBaseURL           string
	WarehouseAudience          string
	WarehouseDelegationEnabled bool
	WarehouseIssueDepartment   string
	WarehouseIssueFailOpen     bool

	// Procurement integration. When ProcurementIntegrationEnabled is true,
	// fleet reads back the sourcing requisition procurement imports from an
	// approved fuel request (origin_system=fleet) so the request detail can
	// show procurement's approval state. The credentials reuse the same
	// SERVICE_CLIENT_* / AUTH_TOKEN_URL as warehouse delegation.
	ProcurementBaseURL            string
	ProcurementAudience           string
	ProcurementIntegrationEnabled bool

	// GateOrderingEnabled turns on SOFT status-ordering for the dispatch chain
	// and the JMP gates: out-of-order transitions (deploy before approval,
	// approving an assignment before the request is approved, completing or
	// approving mileage on a JMP whose dispatch was rejected) return 409 —
	// unless the caller holds the gate-override permission, in which case the
	// bypass is audit-logged. Off by default: gates stay independent until set.
	GateOrderingEnabled bool

	// EnvironmentExplicit records whether ENVIRONMENT/APP_ENV was actually set,
	// as opposed to falling back to the "development" default. The distinction
	// matters for the runtime safeguards below: an unset value on a deployed
	// instance must not be read as a deliberate "this is a dev box".
	EnvironmentExplicit bool

	// Deployed reports that this process looks like a hosted instance rather
	// than a laptop — Railway's injected vars, or gin in release mode (which
	// the Dockerfile sets). Mirrors the signal internal/db uses to reject
	// localhost DSNs.
	Deployed bool
}

// Load reads configuration from env. Hard cutover: no AUTH_MODE, no
// GATEWAY_INTERNAL_SECRET — every inbound request must carry a verifiable
// Bearer token with aud=iag.fleet.
func Load() (Config, error) {
	rawEnv := strings.TrimSpace(envOr("ENVIRONMENT", envOr("APP_ENV", "")))
	env := strings.ToLower(rawEnv)
	if env == "" {
		env = "development"
	}
	issuer := envOr("JWT_ISSUER", "http://localhost:3001")
	cfg := Config{
		EnvironmentExplicit: rawEnv != "",
		Deployed:            deployedRuntime(),
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

		WarehouseBaseURL:           strings.TrimRight(strings.TrimSpace(envOr("WAREHOUSE_BASE_URL", "http://localhost:4005")), "/"),
		WarehouseAudience:          strings.TrimSpace(envOr("WAREHOUSE_AUDIENCE", "iag.warehouse")),
		WarehouseDelegationEnabled: strings.EqualFold(os.Getenv("WAREHOUSE_DELEGATION_ENABLED"), "true"),
		WarehouseIssueDepartment:   strings.TrimSpace(envOr("WAREHOUSE_ISSUE_DEPARTMENT", "fleet-maintenance")),
		WarehouseIssueFailOpen:     strings.EqualFold(os.Getenv("WAREHOUSE_ISSUE_FAIL_OPEN"), "true"),

		ProcurementBaseURL:            strings.TrimRight(strings.TrimSpace(envOr("PROCUREMENT_BASE_URL", "http://localhost:4009")), "/"),
		ProcurementAudience:           strings.TrimSpace(envOr("PROCUREMENT_AUDIENCE", "iag.procurement")),
		ProcurementIntegrationEnabled: strings.EqualFold(os.Getenv("PROCUREMENT_INTEGRATION_ENABLED"), "true"),

		GateOrderingEnabled: strings.EqualFold(os.Getenv("GATE_ORDERING_ENABLED"), "true"),
	}
	if brokers := strings.TrimSpace(os.Getenv("KAFKA_BROKERS")); brokers != "" {
		for _, b := range strings.Split(brokers, ",") {
			if t := strings.TrimSpace(b); t != "" {
				cfg.KafkaBrokers = append(cfg.KafkaBrokers, t)
			}
		}
	}
	if proxies := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); proxies != "" {
		for _, p := range strings.Split(proxies, ",") {
			if t := strings.TrimSpace(p); t != "" {
				cfg.TrustedProxies = append(cfg.TrustedProxies, t)
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
	if c.WarehouseDelegationEnabled {
		if c.WarehouseBaseURL == "" {
			return fmt.Errorf("WAREHOUSE_BASE_URL is required when WAREHOUSE_DELEGATION_ENABLED=true")
		}
		if c.ServiceClientSecret == "" {
			return fmt.Errorf("SERVICE_CLIENT_SECRET is required when WAREHOUSE_DELEGATION_ENABLED=true (needed for service-to-service auth to iag-warehouse)")
		}
	}
	if c.ProcurementIntegrationEnabled {
		if c.ProcurementBaseURL == "" {
			return fmt.Errorf("PROCUREMENT_BASE_URL is required when PROCUREMENT_INTEGRATION_ENABLED=true")
		}
		if c.ServiceClientSecret == "" {
			return fmt.Errorf("SERVICE_CLIENT_SECRET is required when PROCUREMENT_INTEGRATION_ENABLED=true (needed for service-to-service auth to iag-procurement)")
		}
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

// IsProduction gates BOOT-TIME validation (see Validate). It stays strictly
// opt-in via ENVIRONMENT so that a misread signal can never refuse to start a
// running deployment. Runtime safeguards use HardenedRuntime instead.
func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "prod"
}

// isDevLike reports an environment where fail-open behaviour is a deliberate
// convenience rather than an accident.
func (c Config) isDevLike() bool {
	switch c.Environment {
	case "development", "dev", "local", "test":
		return true
	}
	return false
}

// HardenedRuntime reports whether production safeguards apply: fail-closed
// RBAC, and the demo-only endpoints (reset_data, simulate_vehicles) refusing to
// run.
//
// It deliberately does NOT reuse IsProduction. That rule required
// ENVIRONMENT=production, which docs/RAILWAY.md never told anyone to set — so a
// hosted instance defaulted to "development" and ran fail-OPEN, where
// auth.HasPerm grants a permissionless token every permission there is,
// reset_data included. An unset ENVIRONMENT on a deployed instance now hardens
// instead; only an explicit dev-like value opts out.
//
// Unlike the Validate checks this can't prevent boot — the worst case is a
// token that previously got god-mode by accident now getting 403.
func (c Config) HardenedRuntime() bool {
	// An explicit production value always hardens, including on a Config built
	// by hand rather than through Load. Anything else would make the safe
	// setting depend on a bookkeeping flag the caller didn't know to set.
	if c.IsProduction() {
		return true
	}
	if c.EnvironmentExplicit {
		return !c.isDevLike()
	}
	return c.Deployed
}

// StrictRBAC denies access when JWT permissions are empty (fail-closed).
func (c Config) StrictRBAC() bool {
	return c.HardenedRuntime()
}

// deployedRuntime distinguishes a hosted instance from a laptop. Same signal as
// internal/db's localhost-DSN check; GIN_MODE=release is set by the Dockerfile,
// so anything built from it counts.
func deployedRuntime() bool {
	if os.Getenv("RAILWAY_ENVIRONMENT") != "" || os.Getenv("RAILWAY_PROJECT_ID") != "" {
		return true
	}
	return strings.EqualFold(os.Getenv("GIN_MODE"), "release")
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
