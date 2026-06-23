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

func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "prod"
}

// StrictRBAC denies access when JWT permissions are empty (fail-closed).
// Production always enforces strict RBAC; dev allows empty permissions for
// easier local iteration.
func (c Config) StrictRBAC() bool {
	return c.IsProduction()
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
