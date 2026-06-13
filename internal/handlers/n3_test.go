package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// N3: an out-movement exceeding stock still clamps to zero (workshop reality),
// but the over-draw is recorded on the ledger movement instead of being silent.
func TestIntegration_PartOverdrawFlagged(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	repo := store.NewRepository(pool)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	if _, err := repo.Parts.Add(ctx, models.Part{ID: "PT-OD", Name: "Filter", Category: "Filters", SKU: "OD-1", Stock: 3}); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	w := &Workflows{Repo: repo}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "PT-OD"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/parts/PT-OD/movements", bytes.NewReader([]byte(`{"type":"out","qty":10}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	w.partAdjustStock(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("overdraw movement status %d, want 200; %s", rec.Code, rec.Body.String())
	}
	p, err := repo.Parts.Get(ctx, "PT-OD")
	if err != nil {
		t.Fatalf("get part: %v", err)
	}
	if p.Stock != 0 {
		t.Fatalf("stock = %d, want clamped to 0", p.Stock)
	}
	if len(p.Movements) == 0 || !strings.Contains(p.Movements[len(p.Movements)-1].Note, "overdraw") {
		t.Fatalf("expected overdraw note on the movement, got %+v", p.Movements)
	}
}
