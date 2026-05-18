// Package platform wires fleet to shared IAG microservices (health + URLs).
package platform

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

// Services holds base URLs for shared platform microservices.
type Services struct {
	PublicAPIURL      string
	GatewayAPIPrefix  string
	AuthenticationURL string
	NotificationsURL  string
	AccountsURL       string
	CheckDeps         bool
}

// LoadServices reads platform integration URLs from the environment.
func LoadServices() Services {
	return Services{
		PublicAPIURL:      strings.TrimRight(strings.TrimSpace(envOr("PUBLIC_API_URL", "")), "/"),
		GatewayAPIPrefix:  strings.TrimSpace(envOr("GATEWAY_API_PREFIX", "/api/v1/fleet")),
		AuthenticationURL: strings.TrimRight(strings.TrimSpace(envOr("AUTHENTICATION_URL", "")), "/"),
		NotificationsURL:  strings.TrimRight(strings.TrimSpace(envOr("NOTIFICATIONS_URL", "")), "/"),
		AccountsURL:       strings.TrimRight(strings.TrimSpace(envOr("ACCOUNTS_URL", "")), "/"),
		CheckDeps:         strings.EqualFold(envOr("PLATFORM_CHECK_DEPS", "true"), "true"),
	}
}

// DependencyStatus is one upstream readiness probe.
type DependencyStatus struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// CheckReady probes /ready on configured platform services.
func (s Services) CheckReady(ctx context.Context) []DependencyStatus {
	out := make([]DependencyStatus, 0, 4)
	out = append(out, s.probe(ctx, "authentication", s.AuthenticationURL))
	out = append(out, s.probe(ctx, "notifications", s.NotificationsURL))
	out = append(out, s.probe(ctx, "accounts", s.AccountsURL))
	if s.PublicAPIURL != "" {
		out = append(out, s.probe(ctx, "api-gateway", s.PublicAPIURL+"/ready"))
	}
	return out
}

func (s Services) probe(ctx context.Context, name, base string) DependencyStatus {
	if base == "" {
		return DependencyStatus{Name: name, Skipped: true}
	}
	url := base
	if !strings.HasSuffix(url, "/ready") {
		url = strings.TrimRight(url, "/") + "/ready"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DependencyStatus{Name: name, URL: url, OK: false, Error: err.Error()}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return DependencyStatus{Name: name, URL: url, OK: false, Error: err.Error()}
	}
	_ = resp.Body.Close()
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	st := DependencyStatus{Name: name, URL: url, OK: ok}
	if !ok {
		st.Error = resp.Status
	}
	return st
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
