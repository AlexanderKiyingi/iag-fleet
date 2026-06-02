package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/iag/fleet-tool/backend/db"
	"github.com/iag/fleet-tool/backend/internal/cache"
	"github.com/iag/fleet-tool/backend/internal/config"
	pgdb "github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/migrate"
	fleetmw "github.com/iag/fleet-tool/backend/internal/middleware"
	"github.com/iag/fleet-tool/backend/internal/notifications"
	"github.com/iag/fleet-tool/backend/internal/platform"
	"github.com/iag/fleet-tool/backend/internal/platformregister"
	"github.com/iag/fleet-tool/backend/internal/router"
	"github.com/iag/fleet-tool/backend/internal/store"
)

func main() {
	configureLogger()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	if os.Getenv("DATABASE_URL") == "" {
		slog.Error("DATABASE_URL is required (e.g. postgres://user:pass@host:5432/dbname)")
		os.Exit(1)
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := pgdb.Connect(connectCtx, "")
	cancel()
	if err != nil {
		slog.Error("connect Postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := autoMigrate(context.Background(), pool); err != nil {
			slog.Error("auto-migrate failed; refusing to serve", "err", err)
			os.Exit(1)
		}
	} else {
		slog.Info("auto-migrate disabled — assuming schema is current")
	}

	repo := store.NewRepository(pool)
	iotStore := iot.NewStore(pool)
	verifier := authclient.NewVerifier(authclient.Options{
		JWKSURL:  cfg.JWKSURL,
		Issuer:   cfg.JWTIssuer,
		Audience: cfg.Audience,
	})
	// Tolerate transient JWKS failure on boot (auth gateway cold start,
	// network blip, etc.) instead of os.Exit(1) + Railway restart loop.
	// bootstrapJWKS retries with exponential backoff; if the budget is
	// exhausted, the server still starts so /health stays green for
	// Railway, the verifier returns "no verification key" until the
	// background loop succeeds (fails closed), and the background loop
	// continues to retry on its ticker.
	bootstrapJWKS(verifier)
	go jwksRefreshLoop(verifier)
	platformAuth := fleetmw.NewPlatformAuth(fleetmw.PlatformAuthOptions{
		Verifier: verifier,
	})

	appCache := cache.Cache(cache.NoOp{})
	if redisURL := strings.TrimSpace(os.Getenv("REDIS_URL")); redisURL != "" {
		rdb, err := cache.NewRedis(redisURL)
		if err != nil {
			slog.Warn("REDIS_URL set but Redis unavailable; continuing without cache", "err", err)
		} else {
			appCache = rdb
			defer func() { _ = rdb.Close() }()
		}
	}
	iotHub := iot.NewHubFromEnv()
	if strings.TrimSpace(os.Getenv("REDIS_URL")) != "" {
		slog.Info("telemetry hub ready (Redis pub/sub when connected)")
	}

	notifBroker := notifications.NewBroker()

	eventBus := events.New(events.Config{
		Brokers: cfg.KafkaBrokers,
		Enabled: cfg.EventBusEnabled,
	})
	defer func() { _ = eventBus.Close() }()
	if eventBus.Enabled() {
		slog.Info("event bus enabled", "brokers", cfg.KafkaBrokers)
	}

	platformSvc := platform.LoadServices()
	if cfg.PublicAPIURL != "" {
		platformSvc.PublicAPIURL = cfg.PublicAPIURL
	}

	go platformregister.PermissionsLoop(context.Background(), cfg)

	r := router.New(repo, router.Options{
		AllowedOrigin:       cfg.CORSOrigin,
		PlatformAuth:        platformAuth,
		Platform:            platformSvc,
		IoTStore:            iotStore,
		IoTHub:              iotHub,
		RoutingOSRMURL:      strings.TrimSpace(os.Getenv("ROUTING_OSRM_URL")),
		Cache:               appCache,
		TTLDashboard:        durationFromEnvSec("CACHE_TTL_DASHBOARD_SEC", 30),
		TTLAnalytics:        durationFromEnvSec("CACHE_TTL_ANALYTICS_SEC", 45),
		TTLReference:        durationFromEnvSec("CACHE_TTL_REFERENCE_SEC", 600),
		NotificationsBroker: notifBroker,
		Events:              eventBus,
	})

	notifCtx, cancelNotif := context.WithCancel(context.Background())
	defer cancelNotif()
	notifInterval := durationFromEnvSec("NOTIFICATIONS_SCAN_SEC", 60)
	go (&notifications.Producer{
		Repo:   repo,
		Broker: notifBroker,
		Events: eventBus,
	}).Run(notifCtx, notifInterval)
	slog.Info("notifications producer started", "interval", notifInterval)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		_, redisCache := appCache.(*cache.Redis)
		slog.Info("API listening",
			"addr", cfg.Addr,
			"audience", cfg.Audience,
			"corsOrigin", cfg.CORSOrigin,
			"cache", map[bool]string{true: "redis", false: "disabled"}[redisCache],
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		slog.Info("shutdown signal received", "signal", sig.String())
	case err := <-listenErr:
		slog.Error("listener died", "err", err)
		os.Exit(1)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	} else {
		slog.Info("graceful shutdown complete")
	}
}

func configureLogger() {
	var handler slog.Handler
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	default:
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))
}

func durationFromEnvSec(key string, defaultSec int) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return time.Duration(defaultSec) * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return time.Duration(defaultSec) * time.Second
	}
	return time.Duration(n) * time.Second
}

// bootstrapJWKS performs an initial JWKS fetch with exponential backoff
// so a transient failure (auth gateway cold start, network blip,
// Client.Timeout while awaiting headers, etc.) does not crash the
// container. Total budget ~2 minutes. Returns when keys are loaded or
// the budget is exhausted; callers should kick off the background
// refresh loop afterwards either way.
func bootstrapJWKS(v *authclient.Verifier) {
	backoff := time.Second
	const (
		maxBackoff   = 15 * time.Second
		totalBudget  = 2 * time.Minute
		perAttemptTO = 10 * time.Second
	)
	deadline := time.Now().Add(totalBudget)
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(context.Background(), perAttemptTO)
		err := v.Refresh(attemptCtx)
		cancel()
		if err == nil {
			slog.Info("jwks bootstrap ok", "attempt", attempt)
			return
		}
		if time.Now().After(deadline) {
			slog.Error("jwks bootstrap budget exhausted; continuing with empty key set", "attempts", attempt, "err", err)
			return
		}
		slog.Warn("jwks bootstrap failed; retrying", "attempt", attempt, "err", err, "backoff", backoff)
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func jwksRefreshLoop(v *authclient.Verifier) {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		if err := v.Refresh(context.Background()); err != nil {
			slog.Warn("jwks refresh", "err", err)
		}
	}
}

func autoMigrate(parent context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	applied, err := migrate.Up(ctx, pool, db.Migrations())
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if len(applied) == 0 {
		slog.Info("schema already up to date")
	} else {
		slog.Info("schema migrations applied", "count", len(applied), "versions", applied)
	}

	body, err := db.Seed()
	if err != nil {
		return fmt.Errorf("read embedded seed.sql: %w", err)
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("exec seed.sql: %w", err)
		}
		slog.Info("seed.sql applied")
	}
	return nil
}
