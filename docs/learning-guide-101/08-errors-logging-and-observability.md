# Chapter 08 — Errors, Logging and Observability

> **Goal:** centralize error-to-HTTP mapping, emit structured logs with a request ID that follows a request end to end, and expose liveness/readiness probes. By the end, one function decides every HTTP status your API returns, and one log line tells you what happened to any request.

The Spring equivalents are `@ControllerAdvice` + `@ExceptionHandler`, SLF4J + MDC, and Actuator.

---

## 8.1 Sentinels in the domain

Your domain layer defines *what can go wrong*. It should not know about HTTP status codes — that mapping belongs at the edge, so you can reuse the domain errors from a CLI or a worker without dragging in `net/http`.

🧩 `internal/domain/errors.go`:

```go
package domain

import "errors"

// Sentinel errors. Compare with errors.Is, never with ==.
var (
	ErrNotFound           = errors.New("not found")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrConflict           = errors.New("conflict")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailTaken         = errors.New("email already registered")
	ErrAccountInactive    = errors.New("account is not active")
)

// Error carries a machine-readable code and a message safe to show a client,
// optionally wrapping an underlying cause for logging.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap makes errors.Is/errors.As traverse into the wrapped cause.
func (e *Error) Unwrap() error { return e.Err }

// NewError builds a domain error with a client-safe message.
func NewError(code, message string) error {
	return &Error{Code: code, Message: message}
}
```

**Sentinels vs types — when to use which:**

| Use a sentinel (`errors.New`) | Use a type (`*domain.Error`) |
|---|---|
| Callers only need to know *which* condition occurred | Callers need data attached to the error |
| Mapped to a fixed status | Code/message determined at runtime |
| `ErrNotFound`, `ErrForbidden` | A validation error naming the offending field |

Also worth knowing: `errors.Join` (chapter 03) combines multiple errors into one, and `errors.Is` works against **any** of them. That's how `config.Load` reported every missing variable at once.

### Wrapping rules that keep logs useful

```go
// GOOD: adds context, preserves the chain
return fmt.Errorf("get user by email: %w", err)

// BAD: destroys the chain — errors.Is(err, pgx.ErrNoRows) stops working
return fmt.Errorf("get user by email: %v", err)

// BAD: no context. Which user? Which query?
return err
```

Use `%w` when the caller might care about the cause. Use `%v` **only** when you're deliberately terminating the chain at a public boundary and don't want internals to leak. That's rare; default to `%w`.

---

## 8.2 One mapping table to rule them all

This replaces the collection of `@ExceptionHandler` methods you'd normally accumulate.

🧩 `internal/handler/errors.go`:

```go
package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"learn101/internal/domain"
	"learn101/internal/middleware"
)

// errMapping ties a domain sentinel to the HTTP contract.
type errMapping struct {
	err         error
	status      int
	code        string
	publicMsg   string
}

// Order matters only for clarity; errors.Is matches the first entry that fits.
var errMappings = []errMapping{
	{domain.ErrNotFound, http.StatusNotFound, "NOT_FOUND", "resource not found"},
	{domain.ErrInvalidCredentials, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password"},
	{domain.ErrUnauthorized, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required"},
	{domain.ErrForbidden, http.StatusForbidden, "FORBIDDEN", "insufficient privileges"},
	{domain.ErrEmailTaken, http.StatusConflict, "EMAIL_TAKEN", "email is already registered"},
	{domain.ErrConflict, http.StatusConflict, "CONFLICT", "resource conflict"},
	{domain.ErrAccountInactive, http.StatusForbidden, "ACCOUNT_INACTIVE", "account is not active"},
	{context.DeadlineExceeded, http.StatusGatewayTimeout, "TIMEOUT", "request timed out"},
	{context.Canceled, 499, "CLIENT_CLOSED", "client closed the request"},
}

// respondError is the single exit point for handler errors.
func respondError(c *gin.Context, log *slog.Logger, err error) {
	requestID := middleware.RequestIDFrom(c)

	for _, m := range errMappings {
		if errors.Is(err, m.err) {
			// Expected outcome: the client caused it. Not an error in our logs.
			log.Info("request rejected",
				"request_id", requestID,
				"method", c.Request.Method,
				"path", c.FullPath(),
				"code", m.code,
				"status", m.status,
			)
			c.AbortWithStatusJSON(m.status, ErrorResponse{
				Error: &ErrorBody{Code: m.code, Message: m.publicMsg},
			})
			return
		}
	}

	// Unexpected: a bug or an infrastructure failure.
	// Log everything, tell the client nothing.
	log.Error("unhandled error",
		"request_id", requestID,
		"method", c.Request.Method,
		"path", c.FullPath(),
		"err", err,
	)
	c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
		Error: &ErrorBody{
			Code:    "INTERNAL_ERROR",
			Message: "something went wrong",
		},
	})
}
```

The rule that makes this worth doing: **expected errors are logged at INFO, unexpected ones at ERROR.** Your log dashboard then shows a real signal — a spike in `level=ERROR` means a bug, not a user typing a bad password. In Spring terms, this is "don't log the exception in every `@ExceptionHandler` for a 404, but always log the one you didn't anticipate."

### Never leak internals

```go
// What the client sees
{"error":{"code":"INTERNAL_ERROR","message":"something went wrong"}}

// What your log has
level=ERROR msg="unhandled error" request_id=... 
  err="create user: ERROR: duplicate key value violates unique constraint \"core_user_email_key\" (SQLSTATE 23505)"
```

That SQLSTATE error is genuinely useful in logs and genuinely dangerous in a response — it hands an attacker your table and constraint names. The mapping table plus a hardcoded `publicMsg` makes leaking impossible by construction, rather than by remembering.

### Optional: a `FieldError` path

For validation failures you want structured detail (chapter 06's `bindJSON`). Those write their own `422` directly, because "which fields were wrong" is data, not just a code. That's the equivalent of Spring's `MethodArgumentNotValidException` handler.

---

## 8.3 Structured logging with `slog`

`log/slog` (Go 1.21+) is the standard library's answer to SLF4J + a JSON encoder. It's not as configurable as Logback, and it doesn't need to be.

🧩 `internal/logging/logging.go`:

```go
package logging

import (
	"log/slog"
	"os"

	"learn101/internal/config"
)

// New builds the application logger.
func New(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: cfg.LogLevel,
		// Add source file:line. Cheap enough at INFO+, invaluable at 2am.
		AddSource: cfg.LogLevel <= slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Stable, greppable timestamps.
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z"))
			}
			return a
		},
	}

	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
```

JSON in production (machine-parseable, ships to Loki/Datadog/CloudWatch), text locally (readable). The `slog` API is the same either way:

```go
log.Info("user created",
	"user_id", user.ID,
	"email", user.Email,
	"role_id", user.RoleID,
)

log.Error("unhandled error", "request_id", id, "err", err)
```

**Key-value pairs, not string interpolation.** This is the difference between `log.info("User {} created", id)` and MDC-style structured fields — except `slog` makes the structured form the *only* form, so you can't accidentally produce unparseable logs.

| SLF4J / Logback | slog |
|---|---|
| `LoggerFactory.getLogger(X.class)` | `slog.New(...)` once, pass it down |
| `log.info("msg {}", x)` | `log.Info("msg", "x", x)` |
| `logback-spring.xml` | `slog.NewJSONHandler(os.Stdout, opts)` |
| Log levels / `isDebugEnabled()` | `slog.Level`, filtered by the handler |
| **MDC** (`MDC.put("requestId", …)`) | **explicit fields on every call** — no thread-local |
| Logback appenders | an `io.Writer`; stdout is the norm |
| `@Slf4j` | no annotation; a `*slog.Logger` struct field |

The MDC loss is the one real regression: you can't stash a request ID in a context and have it appear in every subsequent log line automatically. Two mitigations:

1. **Pass the logger with fields attached** down the call chain:
   ```go
   reqLog := h.log.With("request_id", middleware.RequestIDFrom(c))
   reqLog.Info("creating user")
   ```
2. Or put the request ID in `context.Context` alongside the identity, and have a helper extract it. Explicit, but no thread locals to reason about.

The logging middleware (below) covers the request boundary, which is 90% of what MDC was doing for you anyway.

---

## 8.4 Request IDs

🧩 `internal/middleware/requestid.go`:

```go
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ContextRequestID = "request.id"
	HeaderRequestID  = "X-Request-ID"
)

// RequestID honours an inbound X-Request-ID (so a client or upstream proxy
// can correlate), or generates one.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(ContextRequestID, id)

		// Echo it back so the Android client can quote it in a bug report.
		c.Writer.Header().Set(HeaderRequestID, id)

		c.Next()
	}
}

func RequestIDFrom(c *gin.Context) string {
	if v, ok := c.Get(ContextRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
```

That one header is the highest-value observability feature you can ship. When a user reports "it failed", they give you the ID, and you `grep request_id=<id>` and see the whole request.

---

## 8.5 Request logging

🧩 `internal/middleware/logger.go`:

```go
package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger emits one structured line per request, after the handler completes.
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next() // run the rest of the chain

		status := c.Writer.Status()
		attrs := []any{
			"request_id", RequestIDFrom(c),
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"response_bytes", c.Writer.Size(),
			"client_ip", c.ClientIP(),
		}
		if query != "" {
			attrs = append(attrs, "query", query)
		}
		if uid, ok := UserID(c); ok {
			attrs = append(attrs, "user_id", uid)
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "gin_errors", c.Errors.String())
		}

		switch {
		case status >= 500:
			log.Error("http_request", attrs...)
		case status >= 400:
			log.Warn("http_request", attrs...)
		default:
			log.Info("http_request", attrs...)
		}
	}
}
```

**Log level by status code** is the trick that makes log volume manageable: `4xx` at WARN (the client's fault), `5xx` at ERROR (yours). Page on the ERROR rate.

**What must never be logged:** passwords, tokens (access or refresh), full `Authorization` headers, and request bodies for auth endpoints. Note that the middleware above logs `query`, which is fine for `?page=2` and dangerous for `?token=...` — if you ever put a credential in a query string, redact it here. This is the Go equivalent of Logback's `%replace` patterns or a `MaskingPatternLayout`.

---

## 8.6 Panic recovery

`gin.Recovery()` catches panics and returns a bare `500`, but it logs unstructured and drops your request ID. Replace it with one that fits your logging:

🧩 `internal/middleware/recovery.go`:

```go
package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery converts a panic into a 500 and logs it with the stack.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered",
					"request_id", RequestIDFrom(c),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"panic", r,
					"stack", string(debug.Stack()),
				)

				// Only write a body if nothing has been written yet.
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
						"error": gin.H{
							"code":    "INTERNAL_ERROR",
							"message": "something went wrong",
						},
					})
				}
			}
		}()

		c.Next()
	}
}
```

The `c.Writer.Written()` guard matters: if the panic happened after a partial response, appending a JSON body produces garbage. Better to abort the connection than to send malformed output.

Recovery must be the **outermost** middleware (registered first) so it catches panics from everything downstream.

---

## 8.7 Health and readiness

Actuator's `/actuator/health` splits into liveness and readiness for good reason: liveness answers "should the orchestrator restart me?", readiness answers "should the load balancer send me traffic?". Conflating them causes restart loops during a brief database blip.

🧩 `internal/handler/health_handler.go`:

```go
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthHandler struct {
	pool *pgxpool.Pool
}

func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{pool: pool}
}

// Live reports that the process is up. No dependencies checked:
// if the DB is down, restarting the app does not help.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "up"})
}

// Ready reports that the process can serve traffic: the DB must answer.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.pool.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":   "degraded",
			"database": "unreachable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "up",
		"database": "up",
	})
}
```

Wire them on the un-versioned path, with no auth:

```go
r.GET("/healthz", healthHandler.Live)
r.GET("/readyz", healthHandler.Ready)
```

| Spring Actuator | Here |
|---|---|
| `/actuator/health` | `/healthz` + `/readyz`, split by purpose |
| `HealthIndicator` beans | a `Ping` call, inlined |
| `/actuator/metrics` | Prometheus + `promhttp` (exercise) |
| `/actuator/info` | `/healthz` returning a version string |
| `/actuator/loggers` (runtime level change) | not available — restart |
| Micrometer | `prometheus/client_golang`, or OpenTelemetry |
| `management.endpoints.web.exposure.include` | you only expose what you register |

Use `/healthz` for the container's liveness probe and `/readyz` for its readiness probe. That distinction is exactly what chapter 11's Compose file will encode.

---

## 8.8 Wiring the middleware stack

Order is behaviour. This is the canonical sequence:

```go
r := gin.New()

r.Use(middleware.Recovery(log))   // 1. outermost: catch panics from anything below
r.Use(middleware.RequestID())     // 2. everything after this can log a request id
r.Use(middleware.Logger(log))     // 3. sees the final status, including 500s
r.Use(corsMiddleware)             // 4. must run early; browsers preflight before auth
```

| Position | Why |
|---|---|
| `Recovery` first | A panic anywhere downstream must still produce a response and a log line. |
| `RequestID` second | Everything after it (including the logger and error mapper) needs the ID. |
| `Logger` third | It wraps the handler, so it records the final status and duration. |
| `CORS` fourth | Preflight `OPTIONS` requests carry no `Authorization` header, so auth must come after. |
| `RequireAuth` / `RequireRole` | Per route group (chapter 07). |

In Spring terms: this is the `Filter` chain order, except it's one readable list instead of `@Order` annotations scattered across classes.

---

## 8.9 Exercises

1. Add a temporary route that panics (`panic("boom")`). Confirm you get a `500` with the standard error body, a `request_id` in the response header, and a log line with a stack trace. Then remove it.
2. `curl -i` a request and confirm `X-Request-ID` comes back. Send your own `X-Request-ID: test-123` and confirm it's echoed, not replaced.
3. Request a nonexistent user and check the log: it should be `level=INFO` with `code=NOT_FOUND`. Then break the DB connection and trigger a `500`: that one should be `level=ERROR`.
4. Cause a unique-constraint violation (`POST /api/v1/users` with an existing email) and confirm the response contains **no** SQL or table names.
5. Stop Postgres and hit `/readyz` → expect `503`. Hit `/healthz` → expect `200`. This is the distinction that prevents restart loops.
6. Set `LOG_LEVEL=debug` and confirm 4xx requests appear at WARN and 5xx at ERROR — and that changing the level needs a restart.
7. Add a Prometheus counter for requests by status code using `prometheus/client_golang`, exposed at `/metrics` (no auth on that path in dev only). This is the Actuator-Micrometer gap you have to fill by hand.

## 8.10 Done when

- [ ] Every handler error goes through `respondError`; none hand-roll `c.JSON(500, ...)`.
- [ ] A `404` and a `500` are distinguishable by log level.
- [ ] No response body ever contains SQL, stack traces, or driver messages.
- [ ] Every response carries `X-Request-ID`, and your logs contain the same value.
- [ ] A deliberate panic returns a clean `500` and logs a stack trace.
- [ ] `/healthz` and `/readyz` disagree when the database is down.

Next: [Chapter 09 — Testing](09-testing.md).
