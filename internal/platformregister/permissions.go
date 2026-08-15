package platformregister

import (
	"context"
	"log/slog"
	"strings"
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
	for attempt := 1; ; attempt++ {
		regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := platformserviceauth.RegisterPermissions(regCtx, saClient, cfg.JWTIssuer, "fleet", perms)
		cancel()
		if err == nil {
			slog.Info("fleet permissions registered with auth service", "count", len(perms), "attempts", attempt)
			return
		}
		// A 401/403 is a configuration fault, not a cold start: SERVICE_CLIENT_ID
		// is missing from the auth service's SERVICE_CLIENT_SECRETS_JSON, or the
		// two secrets disagree. Retrying can't fix it, so say so plainly instead
		// of burying the cause in an endless WARN stream.
		if isClientAuthFailure(err) {
			slog.Error("fleet permissions registration rejected by auth service — check SERVICE_CLIENT_ID/SERVICE_CLIENT_SECRET match the entry in the auth service's SERVICE_CLIENT_SECRETS_JSON",
				"client_id", cfg.ServiceClientID, "token_url", cfg.AuthTokenURL, "err", err)
		} else {
			slog.Warn("fleet permissions registration failed; retrying", "err", err, "backoff", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// isClientAuthFailure reports whether the auth service rejected our
// credentials outright (as opposed to being unreachable or restarting).
func isClientAuthFailure(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "token endpoint 401") ||
		strings.Contains(msg, "token endpoint 403") ||
		strings.Contains(msg, "register status 401") ||
		strings.Contains(msg, "register status 403")
}
