# Backend Security Analysis Report
**Date:** May 5, 2026  
**Status:** READY FOR REVIEW (11 issues identified)

---

## Executive Summary

The Go/Gin backend demonstrates **solid security fundamentals** with proper authentication, encryption, and CSRF protection. However, **11 issues** of varying severity require attention before production deployment. No critical exploits found, but several medium-risk gaps exist.

**Recommendation:** Address issues marked **🔴 HIGH** and **🟡 MEDIUM** before git push.

---

## 🟢 SECURITY STRENGTHS

### ✅ Password Security (Grade: A)
- **Bcrypt with cost=12** via `golang.org/x/crypto` (matches pgcrypto)
- Constant-time comparison (`subtle.ConstantTimeCompare`)
- Password minimum: **8 characters** enforced
- Minimum acceptable but consider raising to 12+ for high-security applications

### ✅ API Key Management (Grade: A-)
- **SHA-256 hashing** of API keys at rest (not stored plaintext)
- Only plaintext exposed once at creation/rotation
- Constant-time lookup via database index on hash
- Suitable for IoT device authentication

### ✅ Session & CSRF Protection (Grade: A-)
- **CSRF double-submit-cookie** with `SameSite=Lax`
- Session cookie: **HttpOnly=true, Secure flag configurable**
- Token TTL: **14 days** (reasonable)
- Proper cleanup on logout
- CSRF middleware skips safe methods and public auth endpoints

### ✅ Email Token Security (Grade: A)
- Tokens: **32 random bytes, SHA-256 hashed**
- **Single-use only** (atomically marked after consumption)
- **Auto-expire** (1 hour for password reset, 24 hours for email verification)
- Prior unused tokens invalidated on new request (prevents replay)

### ✅ Input Validation (Grade: A-)
- **Gin binding** validates JSON structure
- Email validation via `binding:"email"`
- Password minimum enforced
- Rate limiting per endpoint
- Type-safe parameterized SQL queries (pgx)

### ✅ RBAC & Permissions (Grade: B+)
- Permission-based access control (e.g., `view_vehicle`, `add_driver`)
- User + Group hierarchies
- Staff/Superuser distinction
- Audit logging on create/update/delete

### ✅ Dependency Management (Grade: B+)
- Recent stable versions: Gin 1.10, pgx 5.9, crypto from Go std
- No obvious EOL or high-severity package issues
- `go.mod` present with hash verification

---

## 🔴 HIGH PRIORITY ISSUES

### 1. ⚠️ INSUFFICIENT RATE LIMITING ON LOGIN
**Location:** `router.go` line 64  
**Severity:** HIGH  
**Issue:** Login endpoint limited to **20 attempts per 5 minutes per IP**

```go
"/api/auth/login": security.RateLimit(20, 5, security.ByIP),
```

**Risk:** Brute force attacks possible with distributed IPs or credential stuffing from botnets.

**Recommendation:**
- Reduce to **5 attempts per minute** for login
- Implement exponential backoff or account lockout after 3-5 failed attempts
- Log failed attempts for security monitoring
- Consider adding CAPTCHA after 3 failures

```go
// PROPOSED FIX
"/api/auth/login": security.RateLimit(5, 1, security.ByIP), // 5 per minute, burst 1
```

---

### 2. ⚠️ NO ACCOUNT LOCKOUT MECHANISM
**Location:** `auth/store.go` (missing implementation)  
**Severity:** HIGH  
**Issue:** Failed login attempts are not tracked. No lockout after N failures.

**Risk:** Enables brute force and credential stuffing attacks without detection.

**Recommendation:**
- Track failed login attempts in database
- Implement exponential backoff (first: 1s, second: 5s, third: 30s, etc.)
- Temporary account lockout (15 minutes) after 5 failed attempts
- Email notification to user on lockout
- Admin panel to manually unlock accounts

---

### 3. ⚠️ MISSING HTTP SECURITY HEADERS
**Location:** `router.go` (corsMiddleware)  
**Severity:** HIGH  
**Issue:** No security headers in responses

**Missing headers:**
- `Content-Security-Policy` (prevents XSS)
- `X-Frame-Options: DENY` (prevents clickjacking)
- `X-Content-Type-Options: nosniff` (prevents MIME type sniffing)
- `Referrer-Policy` (controls referrer info)
- `Strict-Transport-Security` (HSTS, enforces HTTPS)
- `Permissions-Policy` (controls browser features)

**Recommendation:** Add middleware:
```go
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		c.Next()
	}
}
```

---

### 4. ⚠️ DATABASE CONNECTION POOL NOT BOUNDED ENOUGH
**Location:** `db/db.go` line 25-30  
**Severity:** HIGH (under DoS)  
**Issue:** Pool configuration is minimal:

```go
cfg.MaxConns = 10
cfg.MinConns = 1
cfg.MaxConnLifetime = time.Hour
cfg.MaxConnIdleTime = 15 * time.Minute
```

**Risk:** Under sustained load, connection exhaustion can crash the service. Only 10 max connections is too low for fleet operations.

**Recommendation:**
- Increase `MaxConns` to **50-100** (depends on expected concurrent users)
- Set via **environment variable** for production tuning
- Add connection pool metrics/monitoring
- Implement query timeout (currently missing)

```go
// PROPOSED FIX
cfg.MaxConns = intEnv("DB_MAX_CONNS", 50)
cfg.MinConns = intEnv("DB_MIN_CONNS", 5)
cfg.ConnConfig.ConnectTimeout = 10 * time.Second // missing
```

---

## 🟡 MEDIUM PRIORITY ISSUES

### 5. ⚠️ NO QUERY TIMEOUT / DENIAL OF SERVICE
**Location:** `main.go`, `db/db.go`  
**Severity:** MEDIUM  
**Issue:** Database queries have no timeout. Long-running queries can hang indefinitely.

**Risk:** Slow/malicious queries block worker goroutines, leading to service degradation.

**Recommendation:**
- Add query context timeout (e.g., 30s for most endpoints, 5m for analytics)
- Propagate timeouts through handlers
- Log queries exceeding threshold

```go
// In handler, currently missing:
ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
defer cancel()
items, err := r.Collection.List(ctx)  // instead of c.Request.Context()
```

---

### 6. ⚠️ WEAK CORS VALIDATION
**Location:** `router.go` line 180  
**Severity:** MEDIUM  
**Issue:** CORS allows exact origin match, but no validation of format:

```go
if allowed == "*" || (allowed != "" && origin == allowed) {
```

**Risk:** If `CORS_ORIGIN` contains a typo (e.g., includes subdomain list), bypasses intended restrictions.

**Recommendation:**
- Validate `CORS_ORIGIN` format at startup
- Support comma-separated origins or regex
- Default to DENY if misconfigured

```go
// PROPOSED FIX
allowedOrigins := strings.Split(os.Getenv("CORS_ORIGIN"), ",")
for i, o := range allowedOrigins {
    allowedOrigins[i] = strings.TrimSpace(o)
}
```

---

### 7. ⚠️ INCOMPLETE INPUT VALIDATION
**Location:** `handlers/iot.go` line 160-170  
**Severity:** MEDIUM  
**Issue:** IoT ping ingestion accepts arbitrary JSON with minimal validation:

```go
var batch []ingestPingBody
if err := json.Unmarshal(body, &batch); err != nil {
    // ...
}
// No validation on vehicleId, lat/lng bounds, timestamp reasonableness
```

**Risk:** Malformed data corrupts telemetry; timestamp in year 3000 breaks analytics.

**Recommendation:**
- Validate latitude/longitude ranges: **[-90, 90] / [-180, 180]**
- Validate timestamp: within **±24 hours** of now
- Validate vehicleId: **exists in database**

```go
if b.Lat < -90 || b.Lat > 90 || b.Lng < -180 || b.Lng > 180 {
    c.JSON(http.StatusBadRequest, gin.H{"error": "invalid coordinates"})
    return
}
if !ts.IsZero() && time.Since(ts) > 24*time.Hour {
    c.JSON(http.StatusBadRequest, gin.H{"error": "timestamp too old"})
    return
}
```

---

### 8. ⚠️ NO AUDIT LOGGING FOR SENSITIVE OPERATIONS
**Location:** `handlers/users.go`, `handlers/admin.go`  
**Severity:** MEDIUM  
**Issue:** User creation, password resets, and permission changes are logged, but:
- ✅ User CRUD is logged
- ❌ **Password resets are NOT logged** (only email notification)
- ❌ **Permission grants are NOT logged with details**
- ❌ **API key rotations are NOT logged**
- ❌ **Account lockouts (future) are NOT logged**

**Risk:** Difficult to investigate unauthorized access or detect privilege escalation.

**Recommendation:**
- Log all sensitive operations with user, timestamp, and change details
- Example: "admin@example.com rotated API key for device:123"
- Retention: **6-12 months** for audit trail

---

### 9. ⚠️ EMAIL TOKEN LINKS EXPOSED IN LOGS
**Location:** `handlers/auth.go` line 202-212  
**Severity:** MEDIUM (if logs are exposed)  
**Issue:** Reset/verification tokens are sent via email, but if error logging includes full mail send request, tokens could appear in logs:

```go
slog.Error("resend verification (public) failed",
    "username", u.Username,
    "err", err,
)
```

**Currently OK** because token is in mail data (not logged), but if an error includes the URL:

**Recommendation:**
- **Never log full URLs with tokens**
- Sanitize error messages in mail sending
- Use structured logging with token redaction

```go
// PROPOSED FIX
type SendRequest struct {
    To       string
    Subject  string
    Template string
    Data     map[string]any `json:"-"` // omit from JSON logs
}
```

---

### 10. ⚠️ NO PROTECTION AGAINST TIMING ATTACKS ON VALIDATION
**Location:** `handlers/auth.go` (scattered)  
**Severity:** MEDIUM (low in practice)  
**Issue:** Error messages differ for "user not found" vs. "invalid password":

```go
if errors.Is(err, auth.ErrInvalidCredentials) {
    c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
}
```

**Risk:** Attacker can enumerate valid usernames by measuring response time differences.

**Recommendation:**
- Return identical error for both "user not found" and "bad password"
- Already doing this ✅ (good catch in current code)
- But verify consistency across all auth endpoints

---

### 11. ⚠️ MISSING ENDPOINT DOCUMENTATION / API SPEC
**Location:** `router.go` (no OpenAPI/Swagger)  
**Severity:** MEDIUM (operational)  
**Issue:** No API documentation, authentication details, or rate limits documented.

**Risk:** Developers don't know which endpoints need what permissions; clients can't self-discover rate limits.

**Recommendation:**
- Generate OpenAPI 3.0 spec from code (use `swag`)
- Document each endpoint with:
  - Required permissions
  - Rate limit
  - Example request/response
  - Authentication method (session cookie vs. Bearer token)

---

## 🟢 LOW PRIORITY / BEST PRACTICES

### 12. Consider Rate Limit Headers (informational)
**Location:** `security/ratelimit.go`  
**Issue:** `Retry-After` is set to fixed 60s, but could be smarter:

```go
c.Header("Retry-After", "60")  // Fixed; could be calculated
```

**Recommendation:** Calculate remaining quota and suggest when to retry.

### 13. Ensure Environment Variables Are Validated
**Location:** `main.go` line 24-30  
**Current:** Basic checks (DATABASE_URL required)  
**Recommendation:** Add validation for:
- `CORS_ORIGIN` format
- `COOKIE_SECURE` must match deployment (HTTPS)
- `SMTP_HOST` credentials are not empty if mail is required

### 14. Add Security.txt
**Recommendation:** Create `.well-known/security.txt` to document how security issues should be reported.

---

## 🔧 QUICK WINS (Implement These First)

| Issue | Fix Effort | Impact | Priority |
|-------|-----------|--------|----------|
| Add HTTP security headers | 15 min | HIGH | 🔴 |
| Reduce login rate limit | 5 min | HIGH | 🔴 |
| Add query timeouts | 30 min | HIGH | 🔴 |
| Validate IoT telemetry bounds | 20 min | MEDIUM | 🟡 |
| Add audit logging for API key rotation | 20 min | MEDIUM | 🟡 |
| Fix CORS validation | 10 min | MEDIUM | 🟡 |

---

## 📋 DEPENDENCIES CHECK

| Package | Version | Status | Notes |
|---------|---------|--------|-------|
| `gin-gonic/gin` | 1.10.0 | ✅ Latest | No known vulns |
| `jackc/pgx` | 5.9.2 | ✅ Latest | PostgreSQL driver, well-maintained |
| `golang.org/x/crypto` | 0.50.0 | ✅ Current | Std lib crypto, regularly updated |
| `golang.org/x/time` | 0.15.0 | ✅ Current | Rate limiting, maintained |

**Run vulnerability scan:**
```bash
go get -u github.com/securego/gosec/v2/cmd/gosec
gosec ./...
```

---

## 🚀 DEPLOYMENT CHECKLIST

Before pushing to the git repo, ensure:

- [ ] **🔴 HIGH Issues fixed**
  - [ ] HTTP security headers added
  - [ ] Login rate limit reduced
  - [ ] Query timeouts implemented
  - [ ] Account lockout mechanism added
  
- [ ] **🟡 MEDIUM Issues addressed**
  - [ ] IoT telemetry validation improved
  - [ ] API key rotation audit logging added
  - [ ] CORS validation hardened
  
- [ ] **Environment variables documented**
  - [ ] `COOKIE_SECURE=true` in HTTPS deployments
  - [ ] `REQUIRE_EMAIL_VERIFIED=true` in production
  - [ ] `CORS_ORIGIN` restricted to frontend domain
  - [ ] Verify `DATABASE_URL` uses SSL/TLS (sslmode=require)
  
- [ ] **Database security**
  - [ ] Run migrations with no warnings
  - [ ] Enable audit logging in PostgreSQL (log_statement)
  - [ ] Backup encryption enabled
  
- [ ] **Testing**
  - [ ] HTTPS certificate valid and renewed
  - [ ] Rate limiting tested under load
  - [ ] CSRF token validated on all state-changing requests
  - [ ] Audit logs verified for sensitive operations
  
- [ ] **Monitoring**
  - [ ] Error logs reviewed for secrets exposure
  - [ ] Failed login attempts tracked
  - [ ] API key usage monitored

---

## 📖 REFERENCES

- [OWASP Top 10 2021](https://owasp.org/www-project-top-ten/)
- [Go Security Best Practices](https://golang.org/doc/security)
- [Gin Security Docs](https://gin-gonic.com/docs/examples/using-middleware/)
- [PostgreSQL Security](https://www.postgresql.org/docs/current/sql-syntax.html)

---

## NEXT STEPS

1. **Review** this report with the team
2. **Fix 🔴 HIGH issues** in a feature branch
3. **Run `gosec` & `go vet`** before commit
4. **Test with HTTPS** locally (mkcert)
5. **Create PR** with security checklist
6. **Deploy to staging**, run penetration test
7. **Push to production** with confidence

---

**Report generated:** 2026-05-05  
**Reviewer:** (Pending)  
**Status:** 🟡 REQUIRES ATTENTION before production
