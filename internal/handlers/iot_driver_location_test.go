package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/ctxkeys"
	"github.com/iag/fleet-tool/backend/internal/store"
	"github.com/iag/fleet-tool/backend/internal/testdb"
)

// postDriverLocation drives the handler directly, optionally attaching a
// platform principal (uid == uuid.Nil means "unauthenticated").
func postDriverLocation(h *IoT, uid uuid.UUID, payload any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(payload)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/me/location", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if uid != uuid.Nil {
		c.Set(ctxkeys.UserID, uid)
	}
	h.driverLocation(c)
	return w
}

// postDriverLocationWithEmail attaches both a subject UUID and an email claim,
// exercising the email-bootstrap auto-link path.
func postDriverLocationWithEmail(h *IoT, uid uuid.UUID, email string, payload any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(payload)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/me/location", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(ctxkeys.UserID, uid)
	c.Set(ctxkeys.Claims, &authclient.Claims{Email: email})
	h.driverLocation(c)
	return w
}

// No platform principal on the request → the caller maps to no driver → 403.
// Pure unit test: resolution short-circuits before any DB access.
func TestDriverLocation_noPrincipal403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &IoT{Repo: &store.Repository{}} // non-nil repo; never dereferenced on this path
	w := postDriverLocation(h, uuid.Nil, map[string]any{"lat": 0.35, "lng": 32.59})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d body %q, want 403", w.Code, w.Body.String())
	}
}

// seedLinkedDriver inserts a driver linked to a platform account and returns the uid.
func seedLinkedDriver(t *testing.T, pool *pgxpool.Pool, driverID string) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO drivers (id, name, initials, role, phone, permit_no, permit_class,
			permit_expiry, home_region, status, platform_user_id)
		VALUES ($1, 'Test Driver', 'TD', 'driver', '0700000000', 'P-1', 'B',
			'2030-01-01', 'central', 'on-duty', $2)`,
		driverID, uid)
	if err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	return uid
}

// seedUnlinkedDriver inserts a driver with an email but no platform account link.
func seedUnlinkedDriver(t *testing.T, pool *pgxpool.Pool, driverID, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO drivers (id, name, initials, role, phone, email, permit_no, permit_class,
			permit_expiry, home_region, status)
		VALUES ($1, 'Test Driver', 'TD', 'driver', '0700000001', $2, 'P-2', 'B',
			'2030-01-01', 'central', 'on-duty')`,
		driverID, email)
	if err != nil {
		t.Fatalf("seed unlinked driver: %v", err)
	}
}

func seedActiveJMP(t *testing.T, pool *pgxpool.Pool, driverID, vehicleID string) {
	t.Helper()
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -1).Format("2006-01-02")
	end := now.AddDate(0, 0, 1).Format("2006-01-02")
	_, err := pool.Exec(context.Background(), `
		INSERT INTO jmps (id, vehicle_id, driver_id, purpose, start_date, expected_arrival,
			expected_return, mileage_status, status, created_by)
		VALUES ($1, $2, $3, 'test', $4, $5, $5, 'Pending', 'active', 'test')`,
		"JMP-"+driverID, vehicleID, driverID, start, end)
	if err != nil {
		t.Fatalf("seed jmp: %v", err)
	}
}

func TestIntegration_DriverLocation(t *testing.T) {
	pool, cleanup := testdb.Pool(t)
	defer cleanup()
	ctx := context.Background()
	repo := store.NewRepository(pool)
	h := &IoT{Store: iot.NewStore(pool), Repo: repo}
	gin.SetMode(gin.TestMode)

	// Linked driver + vehicle + active journey covering today.
	v := integrationVehicle("VEH-LOC", "LOC-01")
	v.Status = "offline"
	if _, err := repo.Vehicles.Add(ctx, v); err != nil {
		t.Fatalf("seed vehicle: %v", err)
	}
	uid := seedLinkedDriver(t, pool, "DRV-LOC")
	seedActiveJMP(t, pool, "DRV-LOC", v.ID)

	t.Run("happy path syncs the vehicle", func(t *testing.T) {
		speed := 12.5
		w := postDriverLocation(h, uid, map[string]any{"lat": 0.4000, "lng": 32.6000, "speedKmh": speed})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d body %q, want 200", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"source":"mobile"`) {
			t.Fatalf("ping not tagged mobile: %s", w.Body.String())
		}
		got, err := repo.Vehicles.Get(ctx, v.ID)
		if err != nil {
			t.Fatalf("get vehicle: %v", err)
		}
		if got.Lat != 0.4000 || got.Lng != 32.6000 {
			t.Fatalf("position not synced: lat=%v lng=%v", got.Lat, got.Lng)
		}
		if got.Status != "moving" {
			t.Fatalf("status %q, want moving", got.Status)
		}
		if got.Location != v.Location {
			t.Fatalf("location text changed to %q, want unchanged %q", got.Location, v.Location)
		}
	})

	t.Run("out-of-range coords 400", func(t *testing.T) {
		w := postDriverLocation(h, uid, map[string]any{"lat": 999, "lng": 0})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", w.Code)
		}
	})

	t.Run("no-fix 0,0 rejected 400", func(t *testing.T) {
		w := postDriverLocation(h, uid, map[string]any{"lat": 0, "lng": 0})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", w.Code)
		}
	})

	t.Run("linked driver without active journey 409", func(t *testing.T) {
		uid2 := seedLinkedDriver(t, pool, "DRV-IDLE") // no JMP for this one
		w := postDriverLocation(h, uid2, map[string]any{"lat": 0.40, "lng": 32.60})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d body %q, want 409", w.Code, w.Body.String())
		}
	})

	t.Run("unlinked account 403", func(t *testing.T) {
		w := postDriverLocation(h, uuid.New(), map[string]any{"lat": 0.40, "lng": 32.60})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status %d, want 403", w.Code)
		}
	})

	t.Run("email bootstrap auto-links on first report", func(t *testing.T) {
		v2 := integrationVehicle("VEH-BOOT", "BOOT-01")
		if _, err := repo.Vehicles.Add(ctx, v2); err != nil {
			t.Fatalf("seed vehicle: %v", err)
		}
		seedUnlinkedDriver(t, pool, "DRV-BOOT", "boot@example.com")
		seedActiveJMP(t, pool, "DRV-BOOT", v2.ID)

		uid := uuid.New()
		// Email casing differs from the stored row to prove the match is
		// case-insensitive.
		w := postDriverLocationWithEmail(h, uid, "Boot@Example.com", map[string]any{"lat": 0.41, "lng": 32.61})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d body %q, want 200", w.Code, w.Body.String())
		}
		// The link must now be durable: the driver row carries the subject UUID.
		var linked string
		if err := pool.QueryRow(ctx,
			`SELECT platform_user_id::text FROM drivers WHERE id = 'DRV-BOOT'`).Scan(&linked); err != nil {
			t.Fatalf("read link: %v", err)
		}
		if linked != uid.String() {
			t.Fatalf("platform_user_id %q, want %q", linked, uid.String())
		}
		// A second report (no email claim now) resolves purely by UUID.
		w2 := postDriverLocation(h, uid, map[string]any{"lat": 0.42, "lng": 32.62})
		if w2.Code != http.StatusOK {
			t.Fatalf("second report status %d body %q, want 200", w2.Code, w2.Body.String())
		}
	})

	t.Run("ambiguous email does not auto-link 403", func(t *testing.T) {
		seedUnlinkedDriver(t, pool, "DRV-AMB1", "shared@example.com")
		seedUnlinkedDriver(t, pool, "DRV-AMB2", "shared@example.com")
		w := postDriverLocationWithEmail(h, uuid.New(), "shared@example.com", map[string]any{"lat": 0.41, "lng": 32.61})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status %d, want 403 (ambiguous match must not auto-link)", w.Code)
		}
	})
}
