# Chapter 07 — Authentication, JWT and RBAC

> **Goal:** implement login, issue JWTs, protect routes, and enforce the role model already implied by `core_user_role.role_level`. By the end, `curl` can log in and a protected route rejects unauthenticated requests.

This maps onto Spring Security, which is a big framework. Go's answer is: a bcrypt call, a JWT library, and two middleware functions totalling about 120 lines.

---

## 7.1 A practical problem to solve first

Your migration seeded two users with bcrypt hashes, but **nobody knows their plaintext**. You cannot log in as them.

Two options. The useful one is to set a known password:

🧩 `cmd/hashpw/main.go` — a throwaway generator:

```go
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/hashpw 'the-password'")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash:", err)
		os.Exit(1)
	}
	fmt.Println(string(hash))
}
```

```bash
$ go run ./cmd/hashpw 'Password123!'
$2a$10$Qw8k...

$ psql "$DATABASE_URL" -c "UPDATE core_user SET password = '\$2a\$10\$Qw8k...' WHERE email = 'dyatmarize@cool.app';"
```

Mind the shell escaping — `$` inside double quotes is a variable. Single-quote the SQL, or use `psql` and paste.

Now `dyatmarize@cool.app` / `Password123!` works, and `role_id = 1` makes it a `SUPER_USER` — your most privileged account. The `admin@cool.app` row is `role_id = 2` (`ADMIN`).

Delete `cmd/hashpw` once you've done this, or keep it as a useful dev tool. It's a good example of a second binary in `cmd/`.

---

## 7.2 Password hashing

**Hashing ≠ encryption.** Encryption is reversible with a key. Hashing is one-way by design. You never decrypt a password; you hash the candidate and compare hashes.

bcrypt also:

- incorporates a **random salt**, so the same password produces different hashes
- is deliberately **slow** (the `cost` factor is a log₂ work multiplier)
- is **adaptive** — you can raise the cost as hardware improves

The `$2a$10$` prefix in your seed data means bcrypt, cost 10. Cost 10 is ~50-100ms per hash on modern hardware, which is a good default. Raise to 12 in production if your login latency budget allows.

🧩 `internal/auth/password.go`:

```go
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"learn101/internal/domain"
)

// PasswordHasher is the interface services depend on.
// Implemented by BcryptHasher; replaced by a fake in tests.
type PasswordHasher interface {
	Hash(plain string) (string, error)
	Verify(hash, plain string) error
}

type BcryptHasher struct {
	cost int
}

func NewBcryptHasher() *BcryptHasher {
	// Matches the $2a$10$ prefix already in your seeded rows.
	return &BcryptHasher{cost: bcrypt.DefaultCost}
}

func (h *BcryptHasher) Hash(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if err != nil {
		// bcrypt only uses the first 72 bytes; modern x/crypto returns
		// ErrPasswordTooLong rather than silently truncating.
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func (h *BcryptHasher) Verify(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return domain.ErrInvalidCredentials
	case errors.Is(err, bcrypt.ErrHashTooShort):
		// Stored hash is corrupt — a data problem, not a wrong password.
		return fmt.Errorf("stored password hash is malformed: %w", err)
	default:
		return fmt.Errorf("verify password: %w", err)
	}
}
```

**The 72-byte limit is real.** bcrypt ignores everything past 72 bytes. Newer versions of `x/crypto` return an error instead of truncating silently, which is why `CreateUserRequest.Password` in chapter 06 has `max=72`. If you want to allow longer passphrases, the standard workaround is to pre-hash with SHA-256 (`base64(sha256(password))`) and bcrypt the digest — but don't bother for now; just cap the length.

---

## 7.3 JWT: what you're actually issuing

A JWT is three base64url segments: `header.payload.signature`.

```
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9   .   eyJ1aWQiOjMsInJpZCI6MSwibHZsIjoxfQ   .   dBjftJeZ4CVP...
        header                                   payload (claims)                        signature
```

Critical properties:

- **The payload is readable by anyone.** It is base64, not encryption. Never put secrets in claims. `userID` and `roleLevel` are fine; a password hash is not.
- **The signature proves integrity, not confidentiality.** Anyone with the secret can mint tokens; anyone without it can't forge one.
- **It's stateless.** The server keeps no session. That's the benefit (horizontal scaling, no session store) and the cost (revocation is hard — see §7.7).

### Claims design

Your schema gives you three useful facts: user id, role id, and role level. Put the authorization-relevant ones in the token so protected routes don't need a database round trip.

🧩 `internal/auth/token.go`:

```go
package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"learn101/internal/config"
	"learn101/internal/db"
	"learn101/internal/domain"
)

// TokenType distinguishes an access token from a refresh token.
// Without this, a long-lived refresh token is a valid access token.
type TokenType string

const (
	TokenAccess  TokenType = "access"
	TokenRefresh TokenType = "refresh"
)

const issuerName = "learn101-api"

// Claims is what travels inside the token.
// Embedded jwt.RegisteredClaims supplies iss/sub/exp/iat/nbf/jti.
type Claims struct {
	UserID    int64     `json:"uid"`
	RoleID    int64     `json:"rid"`
	RoleLevel int64     `json:"lvl"`
	TokenType TokenType `json:"typ"`
	jwt.RegisteredClaims
}

type TokenIssuer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewTokenIssuer(cfg *config.Config) *TokenIssuer {
	return &TokenIssuer{
		secret:     []byte(cfg.JWTSecret),
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
	}
}

func (t *TokenIssuer) AccessTTL() time.Duration  { return t.accessTTL }
func (t *TokenIssuer) RefreshTTL() time.Duration { return t.refreshTTL }

// Issue mints a signed token of the given type.
func (t *TokenIssuer) Issue(user db.CoreUser, role db.CoreUserRole, typ TokenType) (string, time.Duration, error) {
	ttl := t.accessTTL
	if typ == TokenRefresh {
		ttl = t.refreshTTL
	}

	now := time.Now()
	subject := ""
	if user.UUID != nil {
		subject = *user.UUID
	}

	claims := Claims{
		UserID:    user.ID,
		RoleID:    role.ID,
		RoleLevel: role.RoleLevel,
		TokenType: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuerName,
			ID:        uuid.NewString(), // jti — useful later for revocation
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", 0, fmt.Errorf("sign token: %w", err)
	}
	return signed, ttl, nil
}

// Parse validates the signature, issuer and expiry, and returns the claims.
func (t *TokenIssuer) Parse(raw string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			// Pin the algorithm. Without this check, an attacker can
			// submit alg=none or swap to RS256 with the public key as HMAC key.
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %q", token.Header["alg"])
			}
			return t.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuerName),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second), // tolerate small clock differences
	)
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if !token.Valid {
		return nil, domain.ErrUnauthorized
	}
	return claims, nil
}
```

Design decisions worth calling out:

| Decision | Why |
|---|---|
| **HS256 (symmetric)** | One service, one secret. Use RS256/ES256 only when a separate service must verify tokens without being able to mint them. |
| **`TokenType` claim** | Prevents a refresh token from being replayed as an access token — a classic oversight. |
| **`jti` (token ID)** | Gives you a handle for a revocation list later. |
| **`sub` = user UUID** | A stable, non-enumerable identifier, matching your schema's `uuid` column. |
| **Short access TTL, long refresh TTL** | 15 minutes / 7 days. The access token is the bearer credential; keep its blast radius small. |
| **`jwt.WithValidMethods`** | Explicit allow-list. This is the fix for the `alg=none` / algorithm-confusion class of attacks. |
| **`WithLeeway(30s)`** | Without it, a 5-second clock skew between your app server and a load balancer causes intermittent 401s that are miserable to debug. |
| **`RoleLevel` in the token** | Lets `RequireRole` work without a DB query. The downside: a role change doesn't take effect until the token expires. Acceptable at 15 minutes; revisit if you ever have a 24-hour access token. |

---

## 7.4 The auth service

🧩 `internal/service/auth_service.go` (essential parts):

```go
package service

import (
	"context"
	"errors"
	"strings"

	"learn101/internal/auth"
	"learn101/internal/db"
	"learn101/internal/domain"
)

// A precomputed bcrypt hash of a random string. Used to burn comparable
// time when the email does not exist, so login timing does not reveal
// whether an account is registered.
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type AuthService struct {
	users  UserStore
	roles  RoleStore
	hasher auth.PasswordHasher
	issuer *auth.TokenIssuer
}

func NewAuthService(users UserStore, roles RoleStore, hasher auth.PasswordHasher, issuer *auth.TokenIssuer) *AuthService {
	return &AuthService{users: users, roles: roles, hasher: hasher, issuer: issuer}
}

func (s *AuthService) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	user, err := s.users.ByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Constant-ish time: do the bcrypt work anyway.
			_ = s.hasher.Verify(dummyHash, password)
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}

	// Every failure below returns the SAME error to the client.
	// Distinguishing "no such user" from "wrong password" is an
	// account-enumeration oracle.
	if user.DeletedAt != nil || user.Status != "ACTIVE" {
		return nil, domain.ErrInvalidCredentials
	}

	if err := s.hasher.Verify(user.Password, password); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	role, err := s.roles.ByID(ctx, user.RoleID)
	if err != nil {
		return nil, err
	}

	access, ttl, err := s.issuer.Issue(user, role, auth.TokenAccess)
	if err != nil {
		return nil, err
	}
	refresh, _, err := s.issuer.Issue(user, role, auth.TokenRefresh)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresIn:    int(ttl.Seconds()),
	}, nil
}

func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.issuer.Parse(refreshToken)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	if claims.TokenType != auth.TokenRefresh {
		return nil, domain.ErrUnauthorized // an access token is not a refresh token
	}

	// Re-read the user so a deleted/role-changed account cannot refresh forever.
	user, err := s.users.ByID(ctx, claims.UserID)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	if user.DeletedAt != nil || user.Status != "ACTIVE" {
		return nil, domain.ErrUnauthorized
	}

	role, err := s.roles.ByID(ctx, user.RoleID)
	if err != nil {
		return nil, err
	}

	access, ttl, err := s.issuer.Issue(user, role, auth.TokenAccess)
	if err != nil {
		return nil, err
	}

	// Return the same refresh token: rotation is an exercise (see 7.7).
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(ttl.Seconds()),
	}, nil
}
```

The `dummyHash` trick and the "one error for every failure" rule are the two things people skip and then regret. Neither costs anything.

### The handler

```go
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if !bindJSON(c, &req) {
		return
	}

	pair, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		// domain.ErrInvalidCredentials maps to 401 (chapter 08)
		respondError(c, h.log, err)
		return
	}

	c.JSON(http.StatusOK, Response[*service.TokenPair]{Data: pair})
}
```

---

## 7.5 Auth middleware

🧩 `internal/middleware/auth.go`:

```go
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"learn101/internal/auth"
)

const (
	ContextUserID    = "auth.user_id"
	ContextUserUUID  = "auth.user_uuid"
	ContextRoleID    = "auth.role_id"
	ContextRoleLevel = "auth.role_level"
)

// RequireAuth validates the bearer token and stores the identity in context.
func RequireAuth(issuer *auth.TokenIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			abort(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing or malformed Authorization header")
			return
		}

		claims, err := issuer.Parse(raw)
		if err != nil {
			abort(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
			return
		}

		// A refresh token must never be accepted as an access credential.
		if claims.TokenType != auth.TokenAccess {
			abort(c, http.StatusUnauthorized, "UNAUTHORIZED", "wrong token type")
			return
		}

		c.Set(ContextUserID, claims.UserID)
		c.Set(ContextUserUUID, claims.Subject)
		c.Set(ContextRoleID, claims.RoleID)
		c.Set(ContextRoleLevel, claims.RoleLevel)

		c.Next()
	}
}

// RequireRole enforces a maximum role_level.
//
// IMPORTANT: role_level in this schema is inverted — SUPER_USER is 1 and
// USER is 3, so LOWER means MORE privileged. "The caller needs at least
// ADMIN" is written as RequireRole(domain.RoleAdmin) => role_level <= 2.
func RequireRole(maxLevel int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		level, ok := RoleLevel(c)
		if !ok {
			abort(c, http.StatusUnauthorized, "UNAUTHORIZED", "no identity in context")
			return
		}
		if level > maxLevel {
			abort(c, http.StatusForbidden, "FORBIDDEN", "insufficient privileges")
			return
		}
		c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

func RoleLevel(c *gin.Context) (int64, bool) {
	v, exists := c.Get(ContextRoleLevel)
	if !exists {
		return 0, false
	}
	level, ok := v.(int64)
	return level, ok
}

func UserID(c *gin.Context) (int64, bool) {
	v, exists := c.Get(ContextUserID)
	if !exists {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}

func UserUUID(c *gin.Context) (string, bool) {
	v, exists := c.Get(ContextUserUUID)
	if !exists {
		return "", false
	}
	uuid, ok := v.(string)
	return uuid, ok
}

func abort(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{"code": code, "message": message},
	})
}

var _ = domain.ErrUnauthorized
```

Two things to note: that last line is a deliberate demonstration of a **compile error you will hit constantly** — Go rejects unused imports and unused variables, so remove both `var _ = ...` and the `domain` import when you type the file. (Java warns; Go refuses to build. You'll grow to like it.) And the `RoleLevel` / `UserID` accessors return `(int64, bool)`, so handlers must handle the missing case rather than getting a silent zero.

### The role constants

🧩 `internal/domain/role.go`:

```go
package domain

// Role levels as seeded in 000001_initial_schema.up.sql.
// Lower role_level == more privileged. This is inverted from intuition.
const (
	RoleSuperUser int64 = 1
	RoleAdmin     int64 = 2
	RoleUser      int64 = 3
)
```

### Which route needs which level

| Route | Middleware | Effective rule |
|---|---|---|
| `POST /auth/login` | none | public |
| `POST /auth/refresh` | none | public (the token is the credential) |
| `GET /healthz`, `/readyz` | none | public |
| `GET /users/me` | `RequireAuth` | any authenticated user |
| `GET /roles` | `RequireAuth` | any authenticated user |
| `GET /users` | `RequireAuth` + `RequireRole(2)` | `role_level <= 2` → ADMIN, SUPER_USER |
| `POST /users` | `RequireAuth` + `RequireRole(2)` | ADMIN, SUPER_USER |
| `GET /users/:uuid` | `RequireAuth` + `RequireRole(2)` | ADMIN, SUPER_USER |
| `PATCH /users/:uuid` | `RequireAuth` + `RequireRole(2)` | ADMIN, SUPER_USER |
| `DELETE /users/:uuid` | `RequireAuth` + `RequireRole(1)` | SUPER_USER only |

Sanity check with your seed data: `dyatmarize` is `role_id = 1` → `role_level = 1` → passes both `<= 2` and `<= 1`. `admin` is `role_id = 2` → `role_level = 2` → passes `<= 2`, fails `<= 1`. Correct.

---

## 7.6 The `Me` endpoint

A useful pattern: read identity from the token, then re-read the row so the response reflects current data (and proves the user still exists).

```go
func (h *UserHandler) Me(c *gin.Context) {
	uuid, ok := middleware.UserUUID(c)
	if !ok {
		respondError(c, h.log, domain.ErrUnauthorized)
		return
	}

	u, err := h.svc.ByUUID(c.Request.Context(), uuid)
	if err != nil {
		respondError(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, Response[UserResponse]{Data: toUserResponse(u)})
}
```

**This is where authorization beyond roles lives.** A `USER` asking for `/users/:uuid` must only ever get their own record — role checks alone don't express that. The rule is: *if a resource belongs to a user, compare the resource owner against the identity in context.* Doing it in the service, not the handler, keeps it testable:

```go
// RoleInfo is the caller's identity: the bits the middleware extracted from
// the token and handed to the service, so the policy is testable without an
// HTTP request in the way.
type RoleInfo struct {
	UserID    int64
	UserUUID  string
	RoleLevel int64
}

func (s *UserService) ByUUIDAs(ctx context.Context, requester RoleInfo, uuid string) (db.CoreUser, error) {
	u, err := s.ByUUID(ctx, uuid)
	if err != nil {
		return db.CoreUser{}, err
	}

	// Non-admins may only read themselves.
	if requester.RoleLevel > domain.RoleAdmin {
		if u.UUID == nil || *u.UUID != requester.UserUUID {
			return db.CoreUser{}, domain.ErrForbidden
		}
	}
	return u, nil
}
```

This is the Go equivalent of Spring Security's `@PreAuthorize("#uuid == authentication.name")` — except it's a normal function you can unit test in three lines.

---

## 7.7 What this design does not do

Know the gaps, so you don't think you're more secure than you are:

| Gap | Impact | Fix |
|---|---|---|
| **No revocation** | A stolen refresh token works for its full TTL, even after logout | Store refresh tokens (hashed) in a table with a `revoked_at`; check on refresh. `jti` is already in your claims for this. |
| **No rotation** | A leaked refresh token stays valid indefinitely as long as it's used before expiry | Issue a new refresh token on each refresh and invalidate the old `jti`; detect reuse as a breach signal. |
| **Single JWT secret** | Compromising it mints tokens for every user | Rotate secrets with a `kid` header; keep two valid keys during rotation. |
| **Role in the token** | A demoted user keeps their old privileges up to `accessTTL` | Short TTL (15 min) mitigates it. Or look up the role per request — privacy vs latency. |
| **No rate limiting on login** | Unlimited password guessing | Per-IP + per-account throttling; lockout after N failures. |
| **No HTTPS enforcement** | A bearer token over plain HTTP is a password on every request | TLS terminates at your ingress; reject non-HTTPS in production. |

An Android client also has a client-side decision to make: store tokens in **`EncryptedSharedPreferences`** (or the Keystore-backed equivalent), never in plain `SharedPreferences`, and never in a file the user can read. There is no cookie store to worry about — it's a bearer token, so physical device security is the whole game.

### Spring Security comparison

| Spring Security | What you write here |
|---|---|
| `SecurityFilterChain` bean | `r.Use(middleware.RequireAuth(issuer))` |
| `AuthenticationManager` / `DaoAuthenticationProvider` | `AuthService.Login` — a plain method |
| `UserDetailsService` | `repo.ByEmail` |
| `BCryptPasswordEncoder` | `auth.BcryptHasher` |
| `@PreAuthorize("hasRole('ADMIN')")` | `middleware.RequireRole(domain.RoleAdmin)` |
| `@EnableMethodSecurity` | nothing — middleware is explicit |
| `SecurityContextHolder` | values in `gin.Context` (per-request, no thread-local) |
| `JwtAuthenticationFilter` | `middleware.RequireAuth` |
| `.sessionManagement().sessionCreationPolicy(STATELESS)` | nothing — there is no session machinery |
| `CsrfFilter` | nothing — no cookies means no CSRF |
| `@WithMockUser` in tests | a hand-written token or a fake `TokenIssuer` |

The pattern is consistent: Spring hands you a configured framework with a lot of behaviour you didn't ask for; Go hands you the five functions and no surprises. Both are legitimate; the Go version is easier to reason about at 2am.

---

## 7.8 Exercises

1. Set a known password for `dyatmarize@cool.app` using `cmd/hashpw`, then log in and decode the returned access token at [jwt.io](https://jwt.io) or with `base64 -d`. Confirm `uid`, `rid`, `lvl`, `typ`, `iss`, `exp`.
2. Call `GET /api/v1/users/me` with and without the `Authorization` header. Confirm `401` without, `200` with.
3. Log in as `admin@cool.app` and call `DELETE /api/v1/users/{some-uuid}`. Confirm `403`. Then try as `dyatmarize` and confirm `200`/`204`.
4. **Exploit your own token type check.** Temporarily remove the `claims.TokenType != auth.TokenAccess` check and replay the refresh token against `/users/me`. It should succeed — that's the vulnerability. Restore the check.
5. Set `JWT_ACCESS_TTL=10s`, log in, wait, and call a protected route. Confirm a clean `401`, not a `500`.
6. Tamper with a token: change one character in the payload segment and send it. Confirm `401`. This proves the signature is actually being verified.
7. Change a user's `role_id` in `psql` and confirm the old token still grants the old level until it expires. Internalize that tradeoff.
8. Add `GET /api/v1/users/:uuid` authorization so a `USER` can only read their own record (see §7.6), and test it with two different accounts.

## 7.9 Done when

- [ ] You can log in with a known password and get an access + refresh token.
- [ ] All five failure modes (bad email, bad password, inactive, soft-deleted, wrong token type) return the **same** 401 body.
- [ ] `RequireRole` correctly blocks `ADMIN` from a SUPER_USER-only route.
- [ ] You have attempted the refresh-token-as-access-token exploit and then fixed it.
- [ ] No `password` value can appear in any response body.

Next: [Chapter 08 — Errors, Logging and Observability](08-errors-logging-and-observability.md).
