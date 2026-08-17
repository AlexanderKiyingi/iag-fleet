package config

import (
	"os"
	"testing"
)

func TestValidate_productionRequiresSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("SERVICE_CLIENT_SECRET", "short")
	t.Setenv("AUTO_MIGRATE", "false")

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected validation error for short secret")
	}
	_ = cfg
}

func TestTelemetrySplit(t *testing.T) {
	c := Config{
		DatabaseURL:          "postgres://a",
		TelemetryDatabaseURL: "postgres://b",
	}
	if !c.TelemetrySplit() {
		t.Fatal("expected split")
	}
	c.TelemetryDatabaseURL = "postgres://a"
	if c.TelemetrySplit() {
		t.Fatal("same URL should not split")
	}
}

func TestStrictRBAC(t *testing.T) {
	if !(Config{Environment: "production"}).StrictRBAC() {
		t.Fatal("production should enable strict RBAC")
	}
	if (Config{Environment: "development"}).StrictRBAC() {
		t.Fatal("development should not enable strict RBAC")
	}
}

// A deployed instance with no ENVIRONMENT set is the exact configuration
// Railway ran: docs/RAILWAY.md never listed the variable. It must harden
// anyway, because fail-open RBAC hands a permissionless token every permission
// in the catalogue.
func TestHardenedRuntime_unsetEnvironmentOnDeployedInstance(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db.example:5432/db")
	os.Unsetenv("ENVIRONMENT")
	os.Unsetenv("APP_ENV")
	t.Setenv("RAILWAY_ENVIRONMENT", "production")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.EnvironmentExplicit {
		t.Fatal("ENVIRONMENT was unset, should not be marked explicit")
	}
	if !cfg.HardenedRuntime() {
		t.Fatal("deployed instance with unset ENVIRONMENT must harden")
	}
	if !cfg.StrictRBAC() {
		t.Fatal("deployed instance must enforce fail-closed RBAC")
	}
}

func TestHardenedRuntime(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		hardened bool
	}{
		{"explicit production", Config{Environment: "production", EnvironmentExplicit: true}, true},
		{"bare production struct", Config{Environment: "production"}, true},
		{"laptop, nothing set", Config{Environment: "development"}, false},
		{"deployed, nothing set", Config{Environment: "development", Deployed: true}, true},
		// A deliberate dev instance on hosted infra stays lenient.
		{"deployed, explicit dev", Config{Environment: "development", EnvironmentExplicit: true, Deployed: true}, false},
		// Anything non-dev that was set on purpose hardens.
		{"explicit staging", Config{Environment: "staging", EnvironmentExplicit: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.HardenedRuntime(); got != tc.hardened {
				t.Fatalf("HardenedRuntime() = %v, want %v", got, tc.hardened)
			}
		})
	}
}

func TestLoad_requiresDatabaseURL(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	_, err := Load()
	if err == nil {
		t.Fatal("expected DATABASE_URL required")
	}
}
