package procurementclient

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

func TestGetRequisitionByOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/requisitions/by-origin" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("system"); got != "fleet" {
			t.Errorf("system query = %q, want fleet", got)
		}
		if got := r.URL.Query().Get("ref"); got != "FREQ-1" {
			t.Errorf("ref query = %q, want FREQ-1", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "PR-2026-0007", "status": "Approved", "total": 480000, "currency": "UGX",
		})
	}))
	defer srv.Close()

	req, err := newTestClient(srv).GetRequisitionByOrigin(context.Background(), "FREQ-1")
	if err != nil {
		t.Fatalf("GetRequisitionByOrigin: %v", err)
	}
	if req.ID != "PR-2026-0007" || req.Status != "Approved" {
		t.Errorf("got %+v", req)
	}
}

func TestGetRequisitionByOrigin_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).GetRequisitionByOrigin(context.Background(), "FREQ-x"); err != ErrRequisitionNotFound {
		t.Fatalf("err = %v, want ErrRequisitionNotFound", err)
	}
}
