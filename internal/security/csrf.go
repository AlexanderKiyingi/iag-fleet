// Package security holds cross-cutting middleware: CSRF protection and
// per-key rate limiting. Both attach to the gin router from
// internal/router and apply selectively based on path / auth shape.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// CSRFCookieName is set by the auth/login handler and read by the
	// frontend; it is intentionally NOT HttpOnly so JS can echo the
	// value into the X-CSRF-Token header on subsequent requests.
	CSRFCookieName = "csrf_token"
	// CSRFHeaderName is the request header the client must echo with.
	CSRFHeaderName = "X-CSRF-Token"
)

// IssueCSRFToken generates a fresh token, sets it on the response as a
// non-HttpOnly cookie, and returns the value. Callers (login handler) can
// also include the value in the response body if they want the client to
// pick it up immediately rather than wait for cookie sync.
func IssueCSRFToken(c *gin.Context, secure bool) string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	tok := base64.RawURLEncoding.EncodeToString(buf)

	// Mirror the session cookie's SameSite policy: cross-origin
	// (secure=true) needs SameSite=None or the browser drops the cookie
	// on XHR from another domain; same-origin dev (secure=false) uses
	// Lax. The CSRF cookie is deliberately readable by JS so the client
	// can echo it in X-CSRF-Token; the session cookie remains HttpOnly.
	c.SetSameSite(sameSite(secure))
	c.SetCookie(CSRFCookieName, tok, 14*24*60*60, "/", "", secure, false)
	return tok
}

// ClearCSRFToken removes the cookie on logout.
func ClearCSRFToken(c *gin.Context) {
	c.SetCookie(CSRFCookieName, "", -1, "/", "", false, false)
}

// sameSite picks the cookie SameSite attribute based on whether the
// connection is HTTPS-only. Cross-origin XHR (Vercel → Railway) requires
// SameSite=None+Secure; same-origin local dev (HTTP) uses Lax. Mirrors
// the helper of the same name in internal/auth so the session and CSRF
// cookies always agree on policy.
func sameSite(secure bool) http.SameSite {
	if secure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

// CSRF returns middleware that enforces double-submit-cookie protection
// on cookie-authenticated state-changing requests. Skipped for:
//   - Safe methods (GET/HEAD/OPTIONS)
//   - Bearer-token requests (the IoT ingest path uses Authorization
//     headers, not cookies, so CSRF doesn't apply)
//   - Paths in skipPrefixes (login, password reset, public endpoints)
//
// On mismatch the request is short-circuited with 403.
func CSRF(skipPrefixes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		path := c.Request.URL.Path
		for _, p := range skipPrefixes {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}
		// Bearer-token requests authenticate without the cookie, so
		// they are not vulnerable to CSRF and need no token.
		if strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
			c.Next()
			return
		}

		cookie, err := c.Cookie(CSRFCookieName)
		header := c.GetHeader(CSRFHeaderName)
		if err != nil || cookie == "" || header == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "csrf token missing"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "csrf token mismatch"})
			return
		}
		c.Next()
	}
}
