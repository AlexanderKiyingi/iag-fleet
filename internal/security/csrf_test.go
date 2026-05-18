package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	// Quiet the gin debug-mode banners during tests; the package-level
	// default is gin.DebugMode which spews route registration to stdout.
	gin.SetMode(gin.TestMode)
}

// newCSRFRouter builds a minimal router with the CSRF middleware in place
// and a single POST handler that always returns 200. Tests vary the
// cookie / header inputs and assert the middleware's response.
func newCSRFRouter(t *testing.T, skip ...string) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.Use(CSRF(skip...))
	r.POST("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.POST("/auth/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestCSRF_AllowsSafeMethods(t *testing.T) {
	t.Parallel()
	r := newCSRFRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET should bypass CSRF, got %d", rec.Code)
	}
}

func TestCSRF_RejectsMissingCookieOrHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cookie string
		header string
	}{
		{"no cookie, no header", "", ""},
		{"cookie only", "abc", ""},
		{"header only", "", "abc"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newCSRFRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/protected", strings.NewReader(""))
			if c.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: c.cookie})
			}
			if c.header != "" {
				req.Header.Set(CSRFHeaderName, c.header)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCSRF_RejectsMismatchedTokens(t *testing.T) {
	t.Parallel()
	r := newCSRFRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/protected", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "expected-value"})
	req.Header.Set(CSRFHeaderName, "different-value")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on token mismatch, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "csrf token mismatch") {
		t.Fatalf("expected mismatch error in body, got %s", rec.Body.String())
	}
}

func TestCSRF_AllowsMatchingTokens(t *testing.T) {
	t.Parallel()
	r := newCSRFRouter(t)
	const tok = "matching-token-value"
	req := httptest.NewRequest(http.MethodPost, "/protected", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tok})
	req.Header.Set(CSRFHeaderName, tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on matching tokens, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestCSRF_BypassesBearerTokenRequests(t *testing.T) {
	t.Parallel()
	// IoT ingest paths authenticate via Authorization: Bearer <key> and
	// don't carry the CSRF cookie at all. The middleware must let them
	// through so devices can POST telemetry without failing csrf checks.
	r := newCSRFRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/protected", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer device-api-key-here")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Bearer-token request should bypass CSRF, got %d", rec.Code)
	}
}

func TestCSRF_BypassesSkipPrefixes(t *testing.T) {
	t.Parallel()
	r := newCSRFRouter(t, "/auth/")
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(""))
	// Deliberately no cookie / header — the skip prefix should let it through.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip-prefix path should bypass CSRF, got %d", rec.Code)
	}
}

// Constant-time compare matters here: a length-leak via early-return
// would let an attacker time their guesses. We check that a token longer
// than the cookie still rejects (rather than panicking or short-circuiting).
func TestCSRF_RejectsLengthMismatchSafely(t *testing.T) {
	t.Parallel()
	r := newCSRFRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/protected", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "short"})
	req.Header.Set(CSRFHeaderName, "much-longer-token-value-here")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("length mismatch should reject, got %d", rec.Code)
	}
}

func TestIssueCSRFToken_SetsCookie(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/issue", func(c *gin.Context) {
		tok := IssueCSRFToken(c, false)
		c.JSON(http.StatusOK, gin.H{"token": tok})
	})
	req := httptest.NewRequest(http.MethodGet, "/issue", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	res := rec.Result()
	defer res.Body.Close()
	var found *http.Cookie
	for _, cc := range res.Cookies() {
		if cc.Name == CSRFCookieName {
			found = cc
			break
		}
	}
	if found == nil {
		t.Fatal("expected CSRF cookie on response")
	}
	if found.Value == "" {
		t.Fatal("CSRF cookie should have a non-empty value")
	}
	if found.HttpOnly {
		t.Fatal("CSRF cookie must NOT be HttpOnly — JS needs to read it for the double-submit pattern")
	}
	if found.MaxAge <= 0 {
		t.Fatalf("CSRF cookie max-age should be positive, got %d", found.MaxAge)
	}
}

func TestIssueCSRFToken_ProducesUniqueValues(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tokens := make(map[string]bool)
	r.GET("/issue", func(c *gin.Context) {
		tok := IssueCSRFToken(c, false)
		tokens[tok] = true
		c.Status(http.StatusOK)
	})
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/issue", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}
	if len(tokens) != 10 {
		t.Fatalf("expected 10 unique tokens, got %d (collisions = critical bug)", len(tokens))
	}
}
