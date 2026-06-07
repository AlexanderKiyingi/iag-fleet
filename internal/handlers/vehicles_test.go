package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/iag/fleet-tool/backend/internal/config"
)

func TestIsUniqueViolation(t *testing.T) {
	err := &pgconn.PgError{Code: "23505"}
	if !isUniqueViolation(err) {
		t.Fatal("expected unique violation")
	}
	if isUniqueViolation(nil) {
		t.Fatal("nil should not be unique violation")
	}
}

func TestRespondError_duplicatePlate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondError(c, &pgconn.PgError{Code: "23505", ConstraintName: "vehicles_plate_key"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
}

func TestDenyIfProduction_blocksInProd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	cfg := config.Config{Environment: "production"}
	if !denyIfProduction(c, cfg, "reset_data") {
		t.Fatal("expected production deny")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
}

func TestDenyIfProduction_allowsDev(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	cfg := config.Config{Environment: "development"}
	if denyIfProduction(c, cfg, "reset_data") {
		t.Fatal("expected dev to pass")
	}
}
