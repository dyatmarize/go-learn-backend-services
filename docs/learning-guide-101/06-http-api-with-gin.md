# Chapter 06 — The HTTP Layer with Gin

> **Goal:** expose your data over a versioned REST API with consistent request validation and responses. By the end, `curl` can list, fetch and create users — and you'll know exactly what changes if you swap Gin for `chi` or the standard library.

This is the chapter that maps most directly onto Spring MVC, so it'll feel familiar. The interesting part is at the end: §6.9 answers your question about which framework matters for an Android client.

---

## 6.1 The framework landscape, honestly

| Framework | What it is | Handlers look like | Who picks it |
|---|---|---|---|
| **Gin** | Most popular Go web framework. Own `gin.Context`, own binding/validation, huge ecosystem. | `func(c *gin.Context)` | Most teams starting out; closest to Spring MVC. |
| **chi** | Thin router on top of `net/http`. ~1000 lines, no context wrapper. | `func(w http.ResponseWriter, r *http.Request)` | Teams that want standard-library-shaped code with better routing. |
| **Echo** | Like Gin but slightly more `net/http`-friendly. | `func(c echo.Context) error` | Teams that want Gin's DX with less culture war. |
| **Fiber** | Built on `fasthttp`, **not** `net/http`. | `func(c *fiber.Ctx) error` | People chasing benchmarks. Avoid as a first framework: `net/http` compatibility (middleware, `httptest`, tooling) matters more than a synthetic speed win. |
| **stdlib `net/http`** | Go 1.22 added method + wildcard routing (`"GET /users/{id}"`). | `func(w http.ResponseWriter, r *http.Request)` | Small services, zero dependencies, maximum learning. |

This guide uses **Gin** because it's the most widely used, and because its binding/validation and route groups map cleanly onto `@RequestBody`/`@Valid` and `@RequestMapping`. §6.9 shows the alternatives so the choice is reversible.

---

## 6.2 Building the router

`gin.Default()` gives you logger + recovery middleware. `gin.New()` gives you nothing, which forces you to be explicit about what runs — the Go way.

🧩 `internal/app/router.go`:

```go
package app

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"learn101/internal/auth"
	"learn101/internal/config"
	"learn101/internal/domain"
	"learn101/internal/handler"
	"learn101/internal/middleware"
)

// NewRouter wires handlers and routes into an engine.
// This is the only place in the app that imports gin.
func NewRouter(
	cfg *config.Config,
	log *slog.Logger,
	issuer *auth.TokenIssuer,
	userHandler *handler.UserHandler,
	authHandler *handler.AuthHandler,
	roleHandler *handler.RoleHandler,
	healthHandler *handler.HealthHandler,
) *gin.Engine {
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())                       // converts panics into 500s
	r.Use(middleware.RequestID())               // chapter 08
	r.Use(middleware.Logger(log))               // chapter 08

	// Liveness / readiness: no auth, no versioning.
	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)

	// Everything versioned lives under /api/v1.
	v1 := r.Group("/api/v1")

	// --- public ---
	v1.POST("/auth/login", authHandler.Login)
	v1.POST("/auth/refresh", authHandler.Refresh)

	// --- any authenticated user ---
	authed := v1.Group("")
	authed.Use(middleware.RequireAuth(issuer))
	{
		authed.GET("/users/me", userHandler.Me)
		authed.GET("/roles", roleHandler.List)
	}

	// --- ADMIN or SUPER_USER (role_level <= 2) ---
	admin := authed.Group("")
	admin.Use(middleware.RequireRole(domain.RoleAdmin))
	{
		admin.GET("/users", userHandler.List)
		admin.POST("/users", userHandler.Create)
		admin.GET("/users/:uuid", userHandler.Get)
		admin.PATCH("/users/:uuid", userHandler.Update)
	}

	// --- SUPER_USER only (role_level <= 1) ---
	superOnly := authed.Group("")
	superOnly.Use(middleware.RequireRole(domain.RoleSuperUser))
	{
		superOnly.DELETE("/users/:uuid", userHandler.Delete)
	}

	return r
}
```

Things to notice, coming from Spring:

- **Routes are registered imperatively, once.** There's no scanner walking the package for `@RequestMapping`. If a route doesn't work, you can read 40 lines and see why.
- **Middleware order is explicit and meaningful.** `RequireAuth` must run before `RequireRole` (which needs the identity in context) — enforced by the nested group, not by an `@Order` annotation you hope is right.
- **`authed.Group("")`** creates a sub-group that inherits the parent's path *and* middleware. Reusing the path prefix is why it's `""` rather than a new segment.
- **Route conflicts panic at startup**, not at request time. `GET /users/:uuid` and `GET /users/me` coexist because Gin's tree prefers the static segment. Registering two identical dynamic paths (`/users/:uuid` and `/users/:id`) will panic on boot — good, you find out immediately.

| Spring MVC | Gin |
|---|---|
| `@RequestMapping("/api/v1")` | `r.Group("/api/v1")` |
| `@GetMapping("/users/{uuid}")` | `v1.GET("/users/:uuid", h.Get)` |
| `@PostMapping` + `@RequestBody` | `v1.POST(..., h.Create)` + `c.ShouldBindJSON` |
| `@RequestParam` / `@PathVariable` | `c.Query("page")` / `c.Param("uuid")` |
| `ResponseEntity<T>` | `c.JSON(http.StatusOK, body)` |
| `@Valid` + `BindingResult` | `binding:"required,email"` tags |
| `Filter` / `HandlerInterceptor` | `gin.HandlerFunc` middleware |
| `@ControllerAdvice` | error middleware (chapter 08) |
| `@Order` on filters | the order you call `Use` in |

---

## 6.3 Handlers: bind → validate → call → respond

A handler should do four things and nothing else. All business logic belongs in the service.

🧩 `internal/handler/user_handler.go`:

```go
package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"learn101/internal/service"
)

type UserHandler struct {
	svc *service.UserService
	log *slog.Logger
}

func NewUserHandler(svc *service.UserService, log *slog.Logger) *UserHandler {
	return &UserHandler{svc: svc, log: log}
}

// List handles GET /api/v1/users?page=1&page_size=20
func (h *UserHandler) List(c *gin.Context) {
	page := parsePage(c.Query("page"), 1)
	pageSize := parsePage(c.Query("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	users, total, err := h.svc.List(c.Request.Context(),
		int32(pageSize),
		int32((page-1)*pageSize),
	)
	if err != nil {
		respondError(c, h.log, err)
		return
	}

	out := make([]UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, toUserResponse(u))
	}

	c.JSON(http.StatusOK, Response[[]UserResponse]{
		Data: out,
		Meta: &Meta{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	})
}

// Get handles GET /api/v1/users/:uuid
func (h *UserHandler) Get(c *gin.Context) {
	u, err := h.svc.ByUUID(c.Request.Context(), c.Param("uuid"))
	if err != nil {
		respondError(c, h.log, err)   // maps domain.ErrNotFound -> 404
		return
	}
	c.JSON(http.StatusOK, Response[UserResponse]{Data: toUserResponse(u)})
}

// Create handles POST /api/v1/users
func (h *UserHandler) Create(c *gin.Context) {
	var req CreateUserRequest
	if !bindJSON(c, &req) {
		return // bindJSON already wrote the 422 (or 400) response
	}

	u, err := h.svc.Create(c.Request.Context(), req.ToInput())
	if err != nil {
		respondError(c, h.log, err)
		return
	}

	c.JSON(http.StatusCreated, Response[UserResponse]{Data: toUserResponse(u)})
}

func parsePage(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

```

```go
// Update handles PATCH /api/v1/users/:uuid
func (h *UserHandler) Update(c *gin.Context) {
	var req UpdateUserRequest
	if !bindJSON(c, &req) {
		return
	}

	u, err := h.svc.Update(c.Request.Context(), c.Param("uuid"), req.ToUpdateInput())
	if err != nil {
		respondError(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, Response[UserResponse]{Data: toUserResponse(u)})
}

// Delete handles DELETE /api/v1/users/:uuid. This is a soft delete:
// the row survives with deleted_at set.
func (h *UserHandler) Delete(c *gin.Context) {
	if err := h.svc.SoftDelete(c.Request.Context(), c.Param("uuid")); err != nil {
		respondError(c, h.log, err)
		return
	}
	c.Status(http.StatusNoContent) // 204: success, no body
}
```

`AuthHandler.Refresh` is the same shape as `Login` — bind, call `svc.Refresh`, return the new token pair — so it isn't repeated here. Three handlers (`List`, `Get`, `Create`) plus these two are every handler body you'll write for this API.

Note the shape of every handler: **bind → validate → call service → respond**. Nothing else.

Also note what's *not* in the import list. Every import here is used; Go rejects unused imports at compile time, unlike Java where they're a warning. Coming from a codebase with 40 auto-inserted imports per file, this feels restrictive for about a day and then feels like a gift.

Note `c.Request.Context()`: that's the `context.Context` from chapter 01. Gin's `*gin.Context` wraps a `*http.Request`; **always pass `c.Request.Context()` down to your service**, never `context.Background()`. Otherwise client disconnects and timeouts don't reach your database query.

---

## 6.4 DTOs and validation

Never expose `db.CoreUser` over HTTP — it contains `Password`. Use explicit request/response types. This is your DTO layer, and it's plain structs with tags.

🧩 `internal/handler/dto.go`:

```go
package handler

import (
	"time"

	"learn101/internal/db"
	"learn101/internal/service"
)

// --- requests ---

type CreateUserRequest struct {
	Name     string `json:"name"     binding:"required,min=2,max=255"`
	Email    string `json:"email"    binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=8,max=72"`
	RoleID   int64  `json:"role_id"  binding:"required,min=1"`
	Status   string `json:"status"   binding:"required,oneof=ACTIVE INACTIVE SUSPENDED"`
}

func (r CreateUserRequest) ToInput() service.CreateUserInput {
	return service.CreateUserInput{
		Name:     r.Name,
		Email:    r.Email,
		Password: r.Password,
		RoleID:   r.RoleID,
		Status:   r.Status,
	}
}

// service.UpdateUserInput mirrors this pointer style: nil means "leave alone".
func (r UpdateUserRequest) ToUpdateInput() service.UpdateUserInput {
	return service.UpdateUserInput{
		Name:   r.Name,
		Email:  r.Email,
		RoleID: r.RoleID,
		Status: r.Status,
	}
}

type UpdateUserRequest struct {
	Name   *string `json:"name"   binding:"omitempty,min=2,max=255"`
	Email  *string `json:"email"  binding:"omitempty,email,max=255"`
	RoleID *int64  `json:"role_id" binding:"omitempty,min=1"`
	Status *string `json:"status" binding:"omitempty,oneof=ACTIVE INACTIVE SUSPENDED"`
}

type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// --- responses ---

type UserResponse struct {
	ID        int64      `json:"id"`
	UUID      string     `json:"uuid"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	RoleID    int64      `json:"role_id"`
	Status    string     `json:"status"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func toUserResponse(u db.CoreUser) UserResponse {
	return UserResponse{
		ID:        u.ID,
		UUID:      deref(u.UUID),
		Name:      u.Name,
		Email:     u.Email,
		RoleID:    u.RoleID,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

**The `binding:` tag is your `@Valid`.** It's backed by `go-playground/validator/v10`, and the tag syntax is worth learning:

| Tag | Meaning | Spring equivalent |
|---|---|---|
| `required` | not the zero value | `@NotNull` / `@NotBlank` |
| `omitempty` | skip other checks if empty (**needed for PATCH** with pointers) | `@Valid` on an optional field |
| `min=8,max=72` | length for strings, value for numbers | `@Size` / `@Min` |
| `email` | email format | `@Email` |
| `oneof=ACTIVE INACTIVE` | enum allow-list | `@Pattern` |
| `uuid` | UUID format | `@Pattern` |
| `gt=0`, `gte=1`, `lte=100` | numeric comparisons | `@Positive`, `@Min` |

**The PATCH pattern deserves attention.** `UpdateUserRequest` uses `*string` / `*int64` so you can distinguish "field absent" from "field set to empty". Java can do this with `JsonNullable` or `Optional`; in Go, a pointer is the idiomatic way, and `omitempty` makes the validator skip absent fields while still validating present ones.

### Handling validation errors

The default `validator` error is unfriendly (`Key: 'CreateUserRequest.Email' Error:Field validation for 'Email' failed on the 'email' tag`). Map it once, in the handler package:

🧩 `internal/handler/binding.go`:

```go
package handler

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// bindJSON decodes and validates a JSON body. On failure it writes the
// response itself and returns false.
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			fields := make([]FieldError, 0, len(ve))
			for _, fe := range ve {
				fields = append(fields, FieldError{
					Field:   fe.Field(),
					Rule:    fe.Tag(),
					Message: validationMessage(fe),
				})
			}
			c.AbortWithStatusJSON(422, ErrorResponse{
				Error: &ErrorBody{
					Code:    "VALIDATION_ERROR",
					Message: "request validation failed",
					Fields:  fields,
				},
			})
			return false
		}

		c.AbortWithStatusJSON(400, ErrorResponse{
			Error: &ErrorBody{Code: "INVALID_JSON", Message: "request body is not valid JSON"},
		})
		return false
	}
	return true
}

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", fe.Field())
	case "email":
		return fmt.Sprintf("%s must be a valid email address", fe.Field())
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", fe.Field(), fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", fe.Field(), fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", fe.Field(), fe.Param())
	default:
		return fmt.Sprintf("%s is invalid", fe.Field())
	}
}
```

`c.ShouldBindJSON` always parses JSON regardless of `Content-Type`. If you'd rather honour content negotiation, use `c.ShouldBind` instead. For a JSON-only API consumed by an Android app, `ShouldBindJSON` is right.

---

## 6.5 A consistent response envelope

Pick one shape and never deviate. Clients written in Retrofit/Kotlin will thank you.

🧩 `internal/handler/response.go`:

```go
package handler

type Response[T any] struct {
	Data  T          `json:"data,omitempty"`
	Meta  *Meta      `json:"meta,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
}

type ErrorResponse struct {
	Error *ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Fields  []FieldError `json:"fields,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type Meta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}
```

Go generics earn their keep here — `Response[UserResponse]` and `Response[[]UserResponse]` share one definition. In Java you'd reach for `ResponseEntity<T>`; same idea, no class per type.

Success:

```json
{
  "data": { "id": 3, "uuid": "…", "name": "dyatmarize", "email": "dyatmarize@cool.app" }
}
```

Success, list:

```json
{
  "data": [ { "id": 3, "…": "…" } ],
  "meta": { "page": 1, "page_size": 20, "total": 3 }
}
```

Failure:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "request validation failed",
    "fields": [ { "field": "Email", "rule": "email", "message": "Email must be a valid email address" } ]
  }
}
```

The `code` string is what your Android app should branch on. Never branch on `message` — it will change.

---

## 6.6 Middleware

A Gin middleware is a function that can do work before and after the handler. It's a `Filter` or `HandlerInterceptor`, with less ceremony.

```go
func RequireJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.ContentType() != "application/json" {
			c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, ErrorResponse{
				Error: &ErrorBody{Code: "UNSUPPORTED_MEDIA_TYPE", Message: "expected application/json"},
			})
			return
		}
		c.Next()   // continue the chain
	}
}
```

The contract:

- `c.Next()` — run the rest of the chain. Code after it runs on the way *back out*.
- `c.AbortWithStatusJSON(...)` — stop the chain and respond. Nothing downstream runs.
- Anything you `c.Set(...)` is readable downstream with `c.Get(...)`.

Order matters and is positional: `r.Use(A, B)` runs A before B, and A's post-`Next()` code runs after B completes.

### CORS for your Android client

An Android app using OkHttp/Retrofit is **not** a browser and is unaffected by CORS. But if you later add a web dashboard or test from a browser, you'll hit it immediately, so add it now behind a config flag:

```bash
$ go get github.com/gin-contrib/cors
```

```go
if !cfg.IsProduction() {
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
}
```

Never use `AllowOrigins: []string{"*"}` together with `AllowCredentials: true` — browsers reject it, and it's a security hole in the making.

---

## 6.7 The service layer

Handlers must not contain business rules. 🧩 `internal/service/user_service.go` (shape, not the whole file):

```go
package service

type CreateUserInput struct {
	Name     string
	Email    string
	Password string
	RoleID   int64
	Status   string
}

type UserService struct {
	repo   UserStore
	hasher PasswordHasher
}

func NewUserService(repo UserStore, hasher PasswordHasher) *UserService {
	return &UserService{repo: repo, hasher: hasher}
}

func (s *UserService) List(ctx context.Context, limit, offset int32) ([]db.CoreUser, int64, error) {
	users, err := s.repo.List(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (s *UserService) Create(ctx context.Context, in CreateUserInput) (db.CoreUser, error) {
	// Business rule: normalized email, checked for uniqueness.
	email := strings.ToLower(strings.TrimSpace(in.Email))

	if _, err := s.repo.ByEmail(ctx, email); err == nil {
		return db.CoreUser{}, domain.ErrEmailTaken
	} else if !errors.Is(err, domain.ErrNotFound) {
		return db.CoreUser{}, err
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return db.CoreUser{}, err
	}

	return s.repo.Create(ctx, db.CreateUserParams{
		RoleID:   in.RoleID,
		Name:     strings.TrimSpace(in.Name),
		Email:    email,
		Password: hash,
		Status:   in.Status,
	})
}
```

This is where `@Service` logic goes: normalization, uniqueness rules, hashing, authorization decisions that aren't purely route-level. The handler stays a translator.

Note the `errors.Is(err, domain.ErrNotFound)` idiom — "I asked if this email exists; a *not found* is the success case."

### The role counterpart

`app.Build` also wires a role trio — the same shape, one file each, so it's shown compactly:

```go
// internal/repository/role_repository.go
type RoleRepository struct{ q *db.Queries }

func NewRoleRepository(pool *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{q: db.New(pool)}
}

func (r *RoleRepository) ByID(ctx context.Context, id int64) (db.CoreUserRole, error) {
	role, err := r.q.GetRoleByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUserRole{}, domain.ErrNotFound
		}
		return db.CoreUserRole{}, fmt.Errorf("get role %d: %w", id, err)
	}
	return role, nil
}

func (r *RoleRepository) List(ctx context.Context) ([]db.CoreUserRole, error) {
	roles, err := r.q.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	return roles, nil
}
```

```go
// internal/service/role_service.go
type RoleStore interface {
	ByID(ctx context.Context, id int64) (db.CoreUserRole, error)
	List(ctx context.Context) ([]db.CoreUserRole, error)
}

type RoleService struct{ repo RoleStore }

func NewRoleService(repo RoleStore) *RoleService { return &RoleService{repo: repo} }

func (s *RoleService) List(ctx context.Context) ([]db.CoreUserRole, error) {
	return s.repo.List(ctx)
}
```

```go
// internal/handler/role_handler.go
type RoleResponse struct {
	ID        int64  `json:"id"`
	RoleName  string `json:"role_name"`
	RoleLevel int64  `json:"role_level"`
}

func toRoleResponses(roles []db.CoreUserRole) []RoleResponse {
	out := make([]RoleResponse, 0, len(roles))
	for _, r := range roles {
		out = append(out, RoleResponse{ID: r.ID, RoleName: r.RoleName, RoleLevel: r.RoleLevel})
	}
	return out
}

type RoleHandler struct {
	svc *service.RoleService
	log *slog.Logger
}

func NewRoleHandler(svc *service.RoleService, log *slog.Logger) *RoleHandler {
	return &RoleHandler{svc: svc, log: log}
}

func (h *RoleHandler) List(c *gin.Context) {
	roles, err := h.svc.List(c.Request.Context())
	if err != nil {
		respondError(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, Response[[]RoleResponse]{Data: toRoleResponses(roles)})
}
```

`AuthService` (chapter 07) depends on the same `RoleStore` so it can look up the caller's role level after login. One small interface, two consumers — which is exactly why it's defined where the consumers are, rather than in the repository package.

---

## 6.8 Wiring it all up

🧩 `internal/app/app.go`:

```go
package app

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"learn101/internal/auth"
	"learn101/internal/config"
	"learn101/internal/handler"
	"learn101/internal/repository"
	"learn101/internal/service"
)

// Build constructs the entire object graph and returns the HTTP engine.
// This is the Go replacement for the Spring container.
func Build(cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) *gin.Engine {
	// data layer
	userRepo := repository.NewUserRepository(pool)
	roleRepo := repository.NewRoleRepository(pool)

	// infrastructure
	hasher := auth.NewBcryptHasher()
	issuer := auth.NewTokenIssuer(cfg)

	// domain services
	userSvc := service.NewUserService(userRepo, hasher)
	authSvc := service.NewAuthService(userRepo, roleRepo, hasher, issuer)
	roleSvc := service.NewRoleService(roleRepo)

	// handlers
	userHandler := handler.NewUserHandler(userSvc, log)
	authHandler := handler.NewAuthHandler(authSvc, log)
	roleHandler := handler.NewRoleHandler(roleSvc, log)
	healthHandler := handler.NewHealthHandler(pool)

	// Every dependency above is used — Go errors on an unused variable,
	// so the wiring cannot silently drift from what the app needs.
	return NewRouter(cfg, log, issuer, userHandler, authHandler, roleHandler, healthHandler)
}
```

Note what's absent: no reflection, no scanning, no `@Autowired`, and — because unused variables are a compile error — no bean can be wired and forgotten. If this function compiles, the graph is complete.

---

## 6.9 If you picked a different framework

Direct answer to the question: **for an Android client, the choice is invisible.**

Your Android app talks to `https://host/api/v1/users` with `Authorization` and `Content-Type` headers and receives JSON. It cannot observe routing internals, middleware shape, or which `Context` type your handlers use. CORS doesn't apply to native app HTTP clients. Nothing about the framework reaches the wire.

So choose on developer experience, not on the client. Here's what actually changes:

**chi** — handlers become plain `net/http`:

```go
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")

	u, err := h.svc.ByUUID(r.Context(), uuid)
	if err != nil {
		writeError(w, h.log, err) // writer-based twin of chapter 08's respondError
		return
	}
	writeJSON(w, http.StatusOK, Response[UserResponse]{Data: toUserResponse(u)})
}

// middleware is a different shape:
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ... validate token from r.Header.Get("Authorization")
		ctx := context.WithValue(r.Context(), ctxKeyUserID, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// routing:
r.Route("/api/v1", func(r chi.Router) {
	r.Post("/auth/login", authHandler.Login)
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(issuer))
		r.Get("/users/me", userHandler.Me)
	})
})
```

**stdlib `net/http` (Go 1.22+)** — no dependency at all:

```go
mux := http.NewServeMux()

mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
mux.HandleFunc("GET /api/v1/users/{uuid}", userHandler.Get)   // {uuid} wildcard
mux.HandleFunc("GET /api/v1/users/me", userHandler.Me)         // more specific wins

// inside the handler:
uuid := r.PathValue("uuid")
```

**What changes across all three:**

| Concern | Gin | chi | stdlib |
|---|---|---|---|
| Handler signature | `func(*gin.Context)` | `func(http.ResponseWriter, *http.Request)` | same as chi |
| Path param | `c.Param("uuid")` | `chi.URLParam(r, "uuid")` | `r.PathValue("uuid")` |
| Read context | `c.Request.Context()` | `r.Context()` | `r.Context()` |
| Write JSON | `c.JSON(200, v)` | `writeJSON(w, 200, v)` yourself | same as chi |
| Error mapping | `respondError(c, log, err)` | a writer-based `writeError(w, log, err)` twin | same as chi |
| Validation | `binding:` tags + `ShouldBindJSON` | `json.NewDecoder` + manual `validator.Struct(v)` | same as chi |
| Middleware | `func(*gin.Context)` + `c.Next()` | `func(http.Handler) http.Handler` | same as chi |
| Route groups | `r.Group("/v1")` | `r.Route("/v1", …)` | manual prefixes |
| Request ID / values | `c.Set` / `c.Get` | `context.WithValue` | `context.WithValue` |

**What does not change — and this is the important part:**

- `internal/service` — all business rules
- `internal/repository` — all your sqlc/pgx code
- `internal/domain` — structs and sentinel errors
- Your DTOs and `binding:` tags (the validator is independent of Gin)
- JWT issuing/parsing, bcrypt, config, logging, migrations, tests of services
- The JSON your Android client receives

In practice, swapping Gin → chi touches `internal/handler` and `internal/app/router.go` and nothing else. That's the payoff of the layering in chapter 02: **the framework is a detail at the edge, not the architecture.**

If you want the maximum learning per hour, the idiomatic progression is: build it with Gin (fastest to a working API), then re-implement one endpoint with stdlib `net/http` in a branch to see what Gin was doing for you. You'll appreciate both more.

---

## 6.10 Exercises

1. `curl` the full CRUD cycle:
   ```bash
   $ curl -s localhost:8080/healthz
   $ curl -s -X POST localhost:8080/api/v1/auth/login \
       -H 'Content-Type: application/json' \
       -d '{"email":"dyatmarize@cool.app","password":"..."}'
   ```
   (You don't know that password — see chapter 07's note. Until then, temporarily add an unauthenticated `POST /api/v1/users` route, or create a user directly in `psql`.)
2. Send a body with a bad email and confirm you get `422` with a `fields` array naming `Email`.
3. Send `{"name":"a"}` and confirm `min=2` fires on `Name`.
4. Send `Content-Type: text/plain` with valid JSON to `ShouldBindJSON` and note that it still parses. Then swap to `c.ShouldBind` and observe the change.
5. Add `GET /api/v1/users?page=2&page_size=5` handling and verify `meta.total` is the unfiltered count while `data` is the page.
6. Request `GET /api/v1/users/999` and confirm a clean `404` (not a 500 and not an empty `200`).
7. Re-implement `GET /api/v1/users/{uuid}` with stdlib `net/http` in a scratch file. Compare the handler bodies side by side.

## 6.11 Done when

- [ ] All routes are registered in one readable file.
- [ ] Every request body is validated, and validation failures return `422` with per-field detail.
- [ ] No handler contains business logic; no handler touches `pgx` or `sqlc` directly.
- [ ] `db.CoreUser` never appears in a JSON response (it contains `Password`).
- [ ] `curl` can create a user and fetch it back.
- [ ] You can state, without looking, what an Android client would need to change to move this API from Gin to chi. (Answer: nothing.)

Next: [Chapter 07 — Authentication, JWT and RBAC](07-auth-jwt-and-rbac.md).
