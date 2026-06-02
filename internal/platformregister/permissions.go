package platformregister

import (
	"context"
	"log/slog"
	"time"

	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"

	"github.com/iag/fleet-tool/backend/internal/config"
	"github.com/iag/fleet-tool/backend/internal/models"
)

// PermissionsLoop registers the fleet permission catalogue with iag-authentication.
func PermissionsLoop(ctx context.Context, cfg config.Config) {
	if cfg.ServiceClientSecret == "" {
		slog.Warn("SERVICE_CLIENT_SECRET unset — skipping fleet permissions registration")
		return
	}
	saClient := platformserviceauth.NewClient(platformserviceauth.Options{
		TokenURL:     cfg.AuthTokenURL,
		ClientID:     cfg.ServiceClientID,
		ClientSecret: cfg.ServiceClientSecret,
		Audience:     "iag.authentication",
	})
	descriptors := models.PermissionDescriptors()
	perms := make([]platformserviceauth.Permission, 0, len(descriptors))
	for _, d := range descriptors {
		perms = append(perms, platformserviceauth.Permission{
			Name:        d.Name,
			Description: d.Description,
		})
	}

	backoff := time.Second
	const maxBackoff = 5 * time.Minute
	for {
		regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := platformserviceauth.RegisterPermissions(regCtx, saClient, cfg.JWTIssuer, "fleet", perms)
		cancel()
		if err == nil {
			slog.Info("fleet permissions registered with auth service", "count", len(perms))
			return
		}
		slog.Warn("fleet permissions registration failed; retrying", "err", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
}
