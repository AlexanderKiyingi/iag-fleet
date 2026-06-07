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

func TestLoad_requiresDatabaseURL(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	_, err := Load()
	if err == nil {
		t.Fatal("expected DATABASE_URL required")
	}
}
