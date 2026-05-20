// Package router wires Gin routes to handlers.
package router

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/cache"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/handlers"
	"github.com/iag/fleet-tool/backend/internal/iot"
	fleetmw "github.com/iag/fleet-tool/backend/internal/middleware"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/platform"
	"github.com/iag/fleet-tool/backend/internal/notifications"
	"github.com/iag/fleet-tool/backend/internal/security"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Options configures the router.
type Options struct {
	AllowedOrigin string
	AuthMode      string
	PlatformAuth  *fleetmw.PlatformAuth
	IoTStore      *iot.Store
	IoTBroker     *iot.Broker
	// RoutingOSRMURL is the origin of an OSRM-compatible service, e.g. https://router.project-osrm.org
	// (no /route suffix). Empty = plan API still works using straight-line fallback only.
	RoutingOSRMURL string
	// Cache is optional response cache (Redis or [cache.NoOp]). Never nil after [New].
	Cache cache.Cache
	// TTLs apply when Cache is Redis; NoOp ignores them.
	TTLDashboard  time.Duration
	TTLAnalytics  time.Duration
	TTLReference  time.Duration
	// NotificationsBroker is the in-process pubsub the producer publishes
	// to and the SSE handler subscribes from. nil falls back to a no-op
	// broker so the routes still register (handy for tests that don't
	// run the producer).
	NotificationsBroker *notifications.Broker
	Events              *events.Bus
	Platform            platform.Services
}

// New builds the gin engine, attaches middleware, and registers all routes.
func New(repo *store.Repository, opts Options) *gin.Engine {
	if opts.Cache == nil {
		opts.Cache = cache.NoOp{}
	}
	if opts.TTLDashboard <= 0 {
		opts.TTLDashboard = 30 * time.Second
	}
	if opts.TTLAnalytics <= 0 {
		opts.TTLAnalytics = 45 * time.Second
	}
	if opts.TTLReference <= 0 {
		opts.TTLReference = 10 * time.Minute
	}

	r := gin.Default()
	r.Use(corsMiddleware(opts.AllowedOrigin))
	r.Use(securityHeaders())
	r.Use(requestTimeout(getRequestTimeout()))
	r.Use(func(c *gin.Context) {
		auth.SetMode(c, opts.AuthMode)
		c.Next()
	})

	health := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	r.GET("/healthz", health)
	r.GET("/health", health)
	r.GET("/ready", health)

	if opts.PlatformAuth != nil {
		r.Use(opts.PlatformAuth.AttachPrincipal())
	}

	api := r.Group("/api")

	// Per-endpoint rate limits. The numbers are conservative defaults;
	// raise via env or a follow-up if real traffic warrants. Keys go to
	// the most discriminating identifier we have for the route. The
	// per-route limiters are stitched onto matching paths below via a
	// small dispatcher (gin doesn't let us reassign middleware after a
	// handler is registered, so we declare a tier middleware that picks
	// the right limiter by path).
	r.Use(perRouteRateLimits(map[string]gin.HandlerFunc{
		"/api/iot/pings": security.RateLimit(120, 30, security.ByIP),
	}, security.RateLimit(300, 60, security.ByIP)))

	(&handlers.Platform{Repo: repo}).Register(api)
	(&handlers.PlatformStatus{Services: opts.Platform, AuthMode: opts.AuthMode}).Register(api)

	// Resource Entity values match the codenames seeded in db/seed.sql so
	// "view_<entity>", "add_<entity>", "change_<entity>", "delete_<entity>"
	// resolve to real permissions.
	(&handlers.Resource[models.Vehicle, *models.Vehicle]{
		Repo: repo, Collection: repo.Vehicles, Entity: "vehicle", IDPrefix: "VEH",
	}).Register(api, "/vehicles")

	(&handlers.Resource[models.Driver, *models.Driver]{
		Repo: repo, Collection: repo.Drivers, Entity: "driver", IDPrefix: "DRV",
	}).Register(api, "/drivers")

	(&handlers.Resource[models.JMP, *models.JMP]{
		Repo: repo, Collection: repo.JMPs, Entity: "jmp", IDPrefix: "JMP",
	}).Register(api, "/jmps")

	(&handlers.Resource[models.Cargo, *models.Cargo]{
		Repo: repo, Collection: repo.Cargo, Entity: "cargo", IDPrefix: "CRG",
	}).Register(api, "/cargo")

	(&handlers.Resource[models.CargoDoc, *models.CargoDoc]{
		Repo: repo, Collection: repo.CargoDocs, Entity: "cargo_doc", IDPrefix: "DOC",
	}).Register(api, "/cargo-docs")

	handlers.NewFuelRecords(repo, opts.Events).Register(api, "/fuel")

	(&handlers.Resource[models.MaintenanceItem, *models.MaintenanceItem]{
		Repo: repo, Collection: repo.Maintenance, Entity: "maintenance_item", IDPrefix: "MX",
	}).Register(api, "/maintenance")

	(&handlers.Resource[models.Part, *models.Part]{
		Repo: repo, Collection: repo.Parts, Entity: "part", IDPrefix: "PRT",
	}).Register(api, "/parts")

	(&handlers.Resource[models.Tyre, *models.Tyre]{
		Repo: repo, Collection: repo.Tyres, Entity: "tyre", IDPrefix: "TYR",
	}).Register(api, "/tyres")

	(&handlers.Resource[models.Trip, *models.Trip]{
		Repo: repo, Collection: repo.Trips, Entity: "trip", IDPrefix: "TRP",
	}).Register(api, "/trips")

	(&handlers.Resource[models.SafetyEvent, *models.SafetyEvent]{
		Repo: repo, Collection: repo.Safety, Entity: "safety_event", IDPrefix: "SAF",
	}).Register(api, "/safety")

	(&handlers.Resource[models.ComplianceItem, *models.ComplianceItem]{
		Repo: repo, Collection: repo.Compliance, Entity: "compliance_item", IDPrefix: "CMP",
	}).Register(api, "/compliance")

	(&handlers.Resource[models.ServiceRequest, *models.ServiceRequest]{
		Repo: repo, Collection: repo.Requests, Entity: "service_request", IDPrefix: "REQ",
	}).Register(api, "/requests")

	(&handlers.Resource[models.TaskItem, *models.TaskItem]{
		Repo: repo, Collection: repo.Tasks, Entity: "task_item", IDPrefix: "TSK",
	}).Register(api, "/tasks")

	(&handlers.Resource[models.DeploymentDay, *models.DeploymentDay]{
		Repo: repo, Collection: repo.Deployment, Entity: "deployment_day", IDPrefix: "DPL",
	}).Register(api, "/deployment")

	(&handlers.Admin{Repo: repo, Cache: opts.Cache}).Register(api)
	(&handlers.Reference{Cache: opts.Cache, TTL: opts.TTLReference}).Register(api)
	(&handlers.Workflows{Repo: repo, Events: opts.Events}).Register(api)
	(&handlers.Dashboard{Repo: repo, Cache: opts.Cache, TTL: opts.TTLDashboard}).Register(api)
	(&handlers.Analytics{Repo: repo, Cache: opts.Cache, TTL: opts.TTLAnalytics}).Register(api)
	(&handlers.Reports{Repo: repo}).Register(api)
	(&handlers.Calendar{Repo: repo}).Register(api)
	(&handlers.FleetLive{Repo: repo}).Register(api)

	// Notifications. Always register so the route table matches the
	// frontend's expectations even if the producer ticker isn't started
	// (e.g. in tests); a missing broker degrades to a never-firing one.
	notifBroker := opts.NotificationsBroker
	if notifBroker == nil {
		notifBroker = notifications.NewBroker()
	}
	(&handlers.Notifications{Repo: repo, Broker: notifBroker}).Register(api)

	(&handlers.Routing{OSRMBaseURL: opts.RoutingOSRMURL}).Register(api)

	// Always attach IoT routes so the route table matches the Next.js client
	// even if a test harness omits a store — handlers return 503 when
	// telemetry is not configured instead of 404-no-route.
	(&handlers.IoT{Store: opts.IoTStore, Broker: opts.IoTBroker, Repo: repo}).Register(api)

	return r
}

// perRouteRateLimits dispatches to a path-specific limiter when one is
// configured, otherwise falls through to the fleet-wide default. We can't
// register middleware after a handler in gin, so this is the cleanest way
// to scope per-endpoint limits without splitting the route group up.
func perRouteRateLimits(byPath map[string]gin.HandlerFunc, fallback gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if mw, ok := byPath[c.Request.URL.Path]; ok {
			mw(c)
			if c.IsAborted() {
				return
			}
			c.Next()
			return
		}
		fallback(c)
	}
}

func corsMiddleware(allowed string) gin.HandlerFunc {
	allowedOrigins := splitAllowedOrigins(allowed)
	allowAny := allowed == "*"
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowAny || (origin != "" && originAllowed(origin, allowedOrigins)) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Operator, X-CSRF-Token")
		c.Header("Access-Control-Expose-Headers", "X-CSRF-Token")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func splitAllowedOrigins(allowed string) []string {
	if allowed == "" || allowed == "*" {
		return nil
	}
	parts := strings.Split(allowed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func originAllowed(origin string, allowed []string) bool {
	for _, candidate := range allowed {
		if origin == candidate {
			return true
		}
	}
	return false
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=(), interest-cohort=()")
		c.Header("X-XSS-Protection", "1; mode=block")
		if c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}
		c.Next()
	}
}

func getRequestTimeout() time.Duration {
	timeout := os.Getenv("REQUEST_TIMEOUT")
	if timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

func requestTimeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if timeout <= 0 || strings.HasSuffix(path, "/track/stream") {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
