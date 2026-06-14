package warehouseclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// noopTokens is a tokenSource that adds no auth — sufficient for httptest.
type noopTokens struct{}

func (noopTokens) AuthorizeRequest(_ context.Context, _ *http.Request) error { return nil }

func newTestClient(srv *httptest.Server) *Client {
	return New(Options{BaseURL: srv.URL, Tokens: noopTokens{}})
}

func TestGetItemBySKU(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sku"); got != "PRT-OIL-01" {
			t.Errorf("sku query = %q, want PRT-OIL-01", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{"id": "11111111-1111-1111-1111-111111111111", "sku": "PRT-OIL-01"}},
		})
	}))
	defer srv.Close()

	item, err := newTestClient(srv).GetItemBySKU(context.Background(), "PRT-OIL-01")
	if err != nil {
		t.Fatalf("GetItemBySKU: %v", err)
	}
	if item.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("item.ID = %q", item.ID)
	}
}

func TestGetItemBySKU_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).GetItemBySKU(context.Background(), "NOPE"); err != ErrItemNotFound {
		t.Fatalf("err = %v, want ErrItemNotFound", err)
	}
}

func TestIssueForDepartment_InsufficientStock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "insufficient stock"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv).IssueForDepartment(context.Background(), IssueRequest{
		Department: "fleet-maintenance",
		Lines:      []IssueLine{{ItemID: "abc", Qty: 5}},
	})
	if err != ErrInsufficientStock {
		t.Fatalf("err = %v, want ErrInsufficientStock", err)
	}
}

func TestIssueForDepartment_SendsIdempotencyKeyAndBody(t *testing.T) {
	var gotKey string
	var gotBody IssueRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "iss-1", "status": "posted"})
	}))
	defer srv.Close()

	iss, err := newTestClient(srv).IssueForDepartment(context.Background(), IssueRequest{
		Department:     "fleet-maintenance",
		WorkOrderRef:   "WO-9",
		Lines:          []IssueLine{{ItemID: "item-1", Qty: 2}},
		IdempotencyKey: "fleet-wo-WO-9",
	})
	if err != nil {
		t.Fatalf("IssueForDepartment: %v", err)
	}
	if iss.Status != "posted" {
		t.Errorf("status = %q", iss.Status)
	}
	if gotKey != "fleet-wo-WO-9" {
		t.Errorf("Idempotency-Key = %q", gotKey)
	}
	if gotBody.WorkOrderRef != "WO-9" || len(gotBody.Lines) != 1 || gotBody.Lines[0].ItemID != "item-1" {
		t.Errorf("decoded body unexpected: %+v", gotBody)
	}
}

func TestOnHandTotal_SumsAvailableOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"qty": 10, "status": "available"},
				{"qty": 4, "status": "hold"},
				{"qty": 6, "status": "available"},
			},
		})
	}))
	defer srv.Close()

	total, err := newTestClient(srv).OnHandTotal(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("OnHandTotal: %v", err)
	}
	if total != 16 {
		t.Errorf("total = %v, want 16 (available only)", total)
	}
}
