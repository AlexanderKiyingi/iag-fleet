// Package procurementclient is fleet's outbound HTTP client for the
// iag-procurement service. Fleet uses it to reconcile a fuel request against
// the sourcing requisition procurement imports from the
// fleet.fuel.request_approved event — i.e. to read back procurement's approval
// state for display on the fuel request.
//
// Authentication uses the platform client_credentials flow: a cached service
// token (aud=iag.procurement) is attached to every request via the shared
// serviceauth client, the same pattern as warehouseclient.
package procurementclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"
)

// ErrRequisitionNotFound is returned when procurement has no requisition imported
// for the given origin reference (HTTP 404) — e.g. the bridge has not yet
// processed the approval event.
var ErrRequisitionNotFound = errors.New("procurement: no requisition for origin ref")

// tokenSource attaches a bearer token to outbound requests. *serviceauth.Client
// satisfies it; tests can substitute a fake.
type tokenSource interface {
	AuthorizeRequest(ctx context.Context, req *http.Request) error
}

// Client talks to a single iag-procurement instance.
type Client struct {
	baseURL string
	http    *http.Client
	tokens  tokenSource
}

// Options configures a Client.
type Options struct {
	BaseURL string
	// Audience is the procurement audience the service token is minted for.
	Audience string
	// TokenURL / ClientID / ClientSecret configure the client_credentials flow.
	TokenURL     string
	ClientID     string
	ClientSecret string
	// HTTPClient overrides the default (10s timeout). Optional.
	HTTPClient *http.Client
	// Tokens overrides the token source. Optional; primarily for tests.
	Tokens tokenSource
}

// New builds a Client. It panics only if the embedded serviceauth client can't
// be constructed (missing credentials) AND no Tokens override was supplied —
// callers should gate construction on config.ProcurementIntegrationEnabled.
func New(opts Options) *Client {
	httpC := opts.HTTPClient
	if httpC == nil {
		httpC = &http.Client{Timeout: 10 * time.Second}
	}
	tokens := opts.Tokens
	if tokens == nil {
		tokens = platformserviceauth.NewClient(platformserviceauth.Options{
			TokenURL:     opts.TokenURL,
			ClientID:     opts.ClientID,
			ClientSecret: opts.ClientSecret,
			Audience:     opts.Audience,
			HTTPClient:   httpC,
		})
	}
	return &Client{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		http:    httpC,
		tokens:  tokens,
	}
}

// Requisition is the subset of a procurement requisition fleet cares about for
// reconciliation.
type Requisition struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"`
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
}

// GetRequisitionByOrigin resolves the requisition procurement imported for a
// fleet origin reference (the FREQ id). Returns ErrRequisitionNotFound when the
// bridge has not yet created one.
func (c *Client) GetRequisitionByOrigin(ctx context.Context, originRef string) (Requisition, error) {
	q := url.Values{}
	q.Set("system", "fleet")
	q.Set("ref", originRef)
	var out Requisition
	err := c.do(ctx, http.MethodGet, "/api/v1/requisitions/by-origin?"+q.Encode(), &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("procurement: build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if err := c.tokens.AuthorizeRequest(ctx, httpReq); err != nil {
		return fmt.Errorf("procurement: authorize: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("procurement: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrRequisitionNotFound
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("procurement: %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("procurement: decode response: %w", err)
		}
	}
	return nil
}
