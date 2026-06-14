// Package warehouseclient is fleet's outbound HTTP client for the iag-warehouse
// ("stores") service, the system-of-record for spare-parts stock under the
// full-delegation model. Fleet uses it to resolve parts to warehouse items, to
// read availability, and to post stock issues when a maintenance work order is
// completed.
//
// Authentication uses the platform client_credentials flow: a cached service
// token (aud=iag.warehouse) is attached to every request via the shared
// serviceauth client, replacing the legacy static internal-secret trust.
package warehouseclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"
)

// ErrInsufficientStock is returned when warehouse rejects an issue because the
// requested quantity exceeds available stock (HTTP 422). Callers decide whether
// to block the WO or fail open.
var ErrInsufficientStock = errors.New("warehouse: insufficient stock")

// ErrItemNotFound is returned when a SKU has no matching warehouse item.
var ErrItemNotFound = errors.New("warehouse: item not found for sku")

// tokenSource attaches a bearer token to outbound requests. *serviceauth.Client
// satisfies it; tests can substitute a fake.
type tokenSource interface {
	AuthorizeRequest(ctx context.Context, req *http.Request) error
}

// Client talks to a single iag-warehouse instance.
type Client struct {
	baseURL string
	http    *http.Client
	tokens  tokenSource
}

// Options configures a Client.
type Options struct {
	BaseURL string
	// Audience is the warehouse audience the service token is minted for.
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
// callers should gate construction on config.WarehouseDelegationEnabled.
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

// Item is the subset of a warehouse item fleet cares about.
type Item struct {
	ID            string  `json:"id"`
	SKU           string  `json:"sku"`
	Name          string  `json:"name"`
	MaterialClass string  `json:"material_class"`
	UOM           string  `json:"uom"`
	MinQty        float64 `json:"min_qty"`
}

// GetItemBySKU resolves a SKU to a warehouse item. Returns ErrItemNotFound when
// no item matches.
func (c *Client) GetItemBySKU(ctx context.Context, sku string) (Item, error) {
	var out struct {
		Items []Item `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/items?sku="+url(sku), "", nil, &out); err != nil {
		return Item{}, err
	}
	if len(out.Items) == 0 {
		return Item{}, ErrItemNotFound
	}
	return out.Items[0], nil
}

// Balance is one on-hand stock balance row for an item.
type Balance struct {
	BinCode string  `json:"bin_code"`
	LotKey  string  `json:"lot_key"`
	Qty     float64 `json:"qty"`
	Status  string  `json:"status"`
}

// GetBalances returns the on-hand balances for a warehouse item id.
func (c *Client) GetBalances(ctx context.Context, itemID string) ([]Balance, error) {
	var out struct {
		Items []Balance `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/items/"+url(itemID)+"/balances", "", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// OnHandTotal returns the authoritative available on-hand quantity for a
// warehouse item, summed across all bins/lots. Used by the event consumer to
// refresh fleet's stock projection idempotently (set, not delta).
func (c *Client) OnHandTotal(ctx context.Context, itemID string) (float64, error) {
	balances, err := c.GetBalances(ctx, itemID)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, b := range balances {
		if b.Status == "" || b.Status == "available" {
			total += b.Qty
		}
	}
	return total, nil
}

// IssueLine is one line of a stock issue. ItemID is the warehouse item UUID;
// BinCode may be empty to let warehouse auto-select an available bin.
type IssueLine struct {
	ItemID  string  `json:"item_id"`
	Qty     float64 `json:"qty"`
	UOM     string  `json:"uom,omitempty"`
	BinCode string  `json:"bin_code,omitempty"`
}

// IssueRequest is the body for POST /issues/for-department (create + post in a
// single atomic warehouse transaction).
type IssueRequest struct {
	Department   string      `json:"department"`
	CostCenter   string      `json:"cost_center,omitempty"`
	WorkOrderRef string      `json:"work_order_ref,omitempty"`
	Notes        string      `json:"notes,omitempty"`
	Lines        []IssueLine `json:"lines"`
	// IdempotencyKey makes a retried issue (e.g. a re-driven WO completion)
	// safe — warehouse returns the original result instead of double-issuing.
	IdempotencyKey string `json:"-"`
}

// Issue is the warehouse response for a posted issue.
type Issue struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// IssueForDepartment creates and posts a stock issue against warehouse. It maps
// a 422 to ErrInsufficientStock so callers can apply their fail-open policy.
func (c *Client) IssueForDepartment(ctx context.Context, req IssueRequest) (Issue, error) {
	var out Issue
	err := c.do(ctx, http.MethodPost, "/api/v1/issues/for-department", req.IdempotencyKey, req, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path, idempotencyKey string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("warehouse: encode body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("warehouse: build request: %w", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")
	if idempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if err := c.tokens.AuthorizeRequest(ctx, httpReq); err != nil {
		return fmt.Errorf("warehouse: authorize: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("warehouse: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return ErrInsufficientStock
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("warehouse: %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("warehouse: decode response: %w", err)
		}
	}
	return nil
}

// url is a tiny query-escape helper kept local so the package has no extra
// surface; warehouse SKUs and UUIDs are simple but may contain reserved chars.
func url(s string) string {
	return strings.NewReplacer(" ", "%20", "?", "%3F", "&", "%26", "#", "%23", "+", "%2B").Replace(s)
}
