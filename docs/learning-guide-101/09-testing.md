# Chapter 09 — Testing

> **Goal:** write tests that catch real regressions — table-driven unit tests, handler tests with `httptest`, and integration tests against a real Postgres. By the end, `go test ./... -race` is your safety net and you trust it.

Coming from JUnit + Mockito + `@SpringBootTest`, three things will feel different:

1. Tests live **next to the code** (`user_service_test.go` beside `user_service.go`), not in a parallel tree.
2. There is **no mocking framework in the standard workflow**. Interfaces are small enough that you write fakes by hand.
3. There is **no test context to boot**. A unit test is a function call. That's why `go test` finishes in milliseconds.

---

## 9.1 The basics

```bash
$ go test ./...                      # all packages
$ go test ./internal/service/...     # one package
$ go test -run TestLogin ./...       # tests matching a regex
$ go test -v ./internal/service/...  # verbose
$ go test -race ./...                # the race detector — always in CI
$ go test -count=1 ./...             # bypass the result cache
$ go test -cover ./...               # coverage summary
$ go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out
```

Rules and conventions:

- File must end in `_test.go`. It is excluded from normal builds.
- Function must be `func TestXxx(t *testing.T)` — capital `Xxx`, `*testing.T`.
- Same `package` as the code under test for white-box tests; `package foo_test` for black-box (imports the package). Use black-box by default: it forces you to test through the public API.
- **No annotations.** No `@Test`. The naming convention *is* the discovery mechanism.
- `t.Fatal`/`t.Fatalf` stops the test; `t.Error`/`t.Errorf` records a failure and continues. Prefer `t.Errorf` so one run reveals every problem.

The result cache is worth knowing about: `go test` caches passing results keyed on inputs. `-count=1` forces a real run. If a test passes locally and fails in CI, suspect cached state or test ordering.

---

## 9.2 Comparing to JUnit

| JUnit 5 | Go |
|---|---|
| `@Test` | `func TestName(t *testing.T)` |
| `@BeforeEach` | a setup call at the top of the test, or `t.Cleanup` |
| `@AfterEach` | `t.Cleanup(func(){...})` — runs on return or panic |
| `@BeforeAll` | `TestMain(m *testing.M)` |
| `@Disabled` | `t.Skip("reason")` |
| `@Nested` | `t.Run("subtest", ...)` |
| `@ParameterizedTest` | a slice of cases + a loop (**table-driven** — §9.3) |
| `@DisplayName` | the test name itself, or `t.Run`'s name |
| `assertThat(x).isEqualTo(y)` | `if x != y { t.Errorf(...) }`, or testify |
| `assertThrows` | check the returned error: `if err == nil { t.Fatal(...) }` |
| `@Mock` / `Mockito.mock()` | a hand-written struct implementing an interface |
| `@InjectMocks` | call `NewService(fakeA, fakeB)` |
| `@SpringBootTest` | `app.Build(cfg, pool, log)` + `httptest` |
| `@DataJpaTest` | a testcontainers Postgres + sqlc (integration) |
| Surefire/Failsafe phases | `*_test.go` vs build-tagged integration tests |
| JaCoCo | `go test -coverprofile` + `go tool cover` |

---

## 9.3 Table-driven tests

This is **the** idiomatic Go test shape. You'll see it in every Go codebase.

```go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"learn101/internal/domain"
	"learn101/internal/middleware"
)

func TestRequireRole(t *testing.T) {
	tests := []struct {
		name       string
		roleLevel  int64
		maxLevel   int64
		wantStatus int
	}{
		{
			name:       "super user passes the admin gate",
			roleLevel:  domain.RoleSuperUser,
			maxLevel:   domain.RoleAdmin,
			wantStatus: http.StatusOK,
		},
		{
			name:       "admin passes the admin gate",
			roleLevel:  domain.RoleAdmin,
			maxLevel:   domain.RoleAdmin,
			wantStatus: http.StatusOK,
		},
		{
			name:       "plain user is blocked by the admin gate",
			roleLevel:  domain.RoleUser,
			maxLevel:   domain.RoleAdmin,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin is blocked by the super-user gate",
			roleLevel:  domain.RoleAdmin,
			maxLevel:   domain.RoleSuperUser,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()

			r.GET("/probe",
				func(c *gin.Context) { c.Set(middleware.ContextRoleLevel, tt.roleLevel) },
				middleware.RequireRole(tt.maxLevel),
				func(c *gin.Context) { c.Status(http.StatusOK) },
			)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
```

Why this shape wins:

- Adding a case is three lines and requires no new function.
- `t.Run` names each case, so a failure reads `TestRequireRole/admin_is_blocked_by_the_super-user_gate` — you know exactly what broke.
- No `@ParameterizedTest` + `@MethodSource` indirection; the cases are a literal, readable slice.
- `t.Run` can be parallelised with `t.Parallel()` inside the subtest when cases are independent.

Note the mid-chain middleware `func(c *gin.Context) { c.Set(...) }` — that's how you inject identity without issuing a token. It's the moral equivalent of `@WithMockUser`.

### The same pattern for error cases

```go
func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		wantOK bool
	}{
		{"empty", "", "", false},
		{"no prefix", "abc123", "", false},
		{"prefix only", "Bearer ", "", false},
		{"lowercase scheme", "bearer abc123", "abc123", true},
		{"valid", "Bearer abc123", "abc123", true},
		{"extra whitespace", "Bearer   abc123  ", "abc123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := middleware.BearerTokenForTest(tt.header)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("token = %q, want %q", got, tt.want)
			}
		})
	}
}
```

`bearerToken` is unexported in `internal/middleware`, so you'd add a tiny `export_test.go` in the **same package** to expose it:

```go
package middleware

// Export a private helper for black-box tests.
var BearerTokenForTest = bearerToken
```

This is the `export_test.go` idiom — a file that exists only during tests, adding no production surface. It's how Go libraries expose internals to tests without making them public API.

---

## 9.4 Fakes instead of mocks

Because interfaces are implicit and small (chapter 01), a fake is ~20 lines. No Mockito, no reflection, no `verify()` DSL.

The interface, defined where it's used:

```go
// in internal/service
type UserStore interface {
	ByID(ctx context.Context, id int64) (db.CoreUser, error)
	ByEmail(ctx context.Context, email string) (db.CoreUser, error)
	ByUUID(ctx context.Context, uuid string) (db.CoreUser, error)
	List(ctx context.Context, limit, offset int32) ([]db.CoreUser, error)
	Count(ctx context.Context) (int64, error)
	Create(ctx context.Context, p db.CreateUserParams) (db.CoreUser, error)
	Update(ctx context.Context, p db.UpdateUserParams) (db.CoreUser, error)
	SoftDelete(ctx context.Context, uuid string) error
}
```

🧩 One wrinkle first: the service tests and the handler tests both need this fake, but a `_test.go` file inside `service_test` is invisible to `handler_test` — test-only packages are not shareable. So the fakes live in a small ordinary package, `internal/testutil`. Only `_test.go` files import it, and nothing in production depends on it.

```go
package testutil

import (
	"context"

	"learn101/internal/db"
	"learn101/internal/domain"
)

// FakeUserStore is an in-memory UserStore. Fields are exported so tests in
// other packages can seed state and inject failures.
type FakeUserStore struct {
	Users  map[int64]db.CoreUser
	ByMail map[string]int64
	NextID int64

	// Injected failures, so you can test error paths.
	ErrOnCreate error
	ErrOnList   error
}

func NewFakeUserStore(users ...db.CoreUser) *FakeUserStore {
	f := &FakeUserStore{
		Users:  map[int64]db.CoreUser{},
		ByMail: map[string]int64{},
		NextID: 1,
	}
	for _, u := range users {
		f.Users[u.ID] = u
		f.ByMail[u.Email] = u.ID
		if u.ID >= f.NextID {
			f.NextID = u.ID + 1
		}
	}
	return f
}

func (f *FakeUserStore) ByID(ctx context.Context, id int64) (db.CoreUser, error) {
	u, ok := f.Users[id]
	if !ok {
		return db.CoreUser{}, domain.ErrNotFound
	}
	return u, nil
}

func (f *FakeUserStore) ByEmail(ctx context.Context, email string) (db.CoreUser, error) {
	id, ok := f.ByMail[email]
	if !ok {
		return db.CoreUser{}, domain.ErrNotFound
	}
	return f.Users[id], nil
}

func (f *FakeUserStore) Create(ctx context.Context, p db.CreateUserParams) (db.CoreUser, error) {
	if f.ErrOnCreate != nil {
		return db.CoreUser{}, f.ErrOnCreate
	}
	if _, exists := f.ByMail[p.Email]; exists {
		return db.CoreUser{}, domain.ErrEmailTaken
	}

	uuid := "generated-uuid"
	u := db.CoreUser{
		ID:       f.NextID,
		RoleID:   p.RoleID,
		Name:     p.Name,
		Email:    p.Email,
		Password: p.Password,
		Status:   p.Status,
		UUID:     &uuid,
	}
	f.Users[u.ID] = u
	f.ByMail[u.Email] = u.ID
	f.NextID++
	return u, nil
}

// ... the remaining methods, each 5-10 lines
```

Two things this gets you that Mockito makes awkward:

- **State, not just interactions.** The fake is a real (in-memory) implementation, so a test can create a user and then fetch it, exercising the flow rather than asserting a call sequence.
- **Error injection as a field.** `store.ErrOnCreate = errors.New("boom")` tests the failure branch without `when(...).thenThrow(...)` and without a mocking DSL.

A fake hasher makes password logic testable without paying bcrypt's cost:

```go
// Also in internal/testutil.
type FakeHasher struct{}

func (FakeHasher) Hash(plain string) (string, error) { return "hashed:" + plain, nil }
func (FakeHasher) Verify(hash, plain string) error {
	if hash != "hashed:"+plain {
		return domain.ErrInvalidCredentials
	}
	return nil
}
```

### testify: less noise, same model

`github.com/stretchr/testify` is the one test dependency nearly every Go project has. It doesn't mock anything by default; it just makes assertions readable.

```go
package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"learn101/internal/db"
	"learn101/internal/domain"
	"learn101/internal/service"
	"learn101/internal/testutil"
)

func TestUserService_Create(t *testing.T) {
	store := testutil.NewFakeUserStore()
	svc := service.NewUserService(store, testutil.FakeHasher{})

	got, err := svc.Create(context.Background(), service.CreateUserInput{
		Name:     "Budi",
		Email:    "BUDI@Example.com ",
		Password: "secret123",
		RoleID:   domain.RoleUser,
		Status:   "ACTIVE",
	})

	require.NoError(t, err)          // stop now if this fails
	assert.Equal(t, "budi@example.com", got.Email)  // normalized
	assert.Equal(t, "hashed:secret123", got.Password)
	assert.NotZero(t, got.ID)
}

func TestUserService_Create_DuplicateEmail(t *testing.T) {
	store := testutil.NewFakeUserStore(db.CoreUser{ID: 1, Email: "budi@example.com"})
	svc := service.NewUserService(store, testutil.FakeHasher{})

	_, err := svc.Create(context.Background(), service.CreateUserInput{
		Email: "budi@example.com", Password: "secret123", RoleID: 3, Status: "ACTIVE", Name: "Budi",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrEmailTaken)
}
```

**`require` vs `assert`:** `require` calls `t.FailNow()` — use it when continuing would cause a confusing secondary failure (nil deref). `assert` records and continues — use it for independent checks. `assert.ErrorIs` is the idiomatic way to test wrapped sentinels, and it's what you'll use more than anything else.

Note the test above caught a real behaviour: `"BUDI@Example.com "` → `"budi@example.com"`. That's the service's normalization, and it's exactly the kind of thing a rolling refactor breaks silently.

---

## 9.5 Testing handlers with `httptest`

No server, no port, no network. `httptest` runs your handler in-process.

```go
package handler_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"learn101/internal/handler"
	"learn101/internal/service"
	"learn101/internal/testutil"
)

func TestUserHandler_Create_Validation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Discard logs so test output stays readable.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Same fakes as the service tests — that sharing is exactly why
	// internal/testutil exists.
	svc := service.NewUserService(testutil.NewFakeUserStore(), testutil.FakeHasher{})
	h := handler.NewUserHandler(svc, log)

	r := gin.New()
	r.POST("/users", h.Create)

	body := `{"name":"a","email":"not-an-email","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var resp handler.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "VALIDATION_ERROR", resp.Error.Code)

	fields := map[string]string{}
	for _, f := range resp.Error.Fields {
		fields[f.Field] = f.Rule
	}
	assert.Equal(t, "min", fields["Name"])
	assert.Equal(t, "email", fields["Email"])
	assert.Equal(t, "min", fields["Password"])
}
```

This is the equivalent of `MockMvc` / `WebTestClient`, minus the context boot. Because the handler only depends on an interface, the whole test is a function call plus a recorder. It runs in microseconds.

**What to test at this layer:** status codes, response envelope shape, validation error details, auth rejection, and the domain-error → status mapping. Do **not** re-test business rules here — those belong in service tests.

### Testing middleware end to end

```go
// ptr is a tiny generic helper — a very common Go test utility, since
// you cannot take the address of a literal.
func ptr[T any](v T) *T { return &v }

func TestAuthFlow(t *testing.T) {
	issuer := auth.NewTokenIssuer(&config.Config{
		JWTSecret: "0123456789012345678901234567890123456789",
		AccessTTL: 15 * time.Minute,
	})
	token, _, err := issuer.Issue(
		db.CoreUser{ID: 1, UUID: ptr("u-1")},
		db.CoreUserRole{ID: domain.RoleAdmin, RoleLevel: domain.RoleAdmin},
		auth.TokenAccess,
	)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	authed := r.Group("")
	authed.Use(middleware.RequireAuth(issuer))
	authed.GET("/me", func(c *gin.Context) {
		id, ok := middleware.UserID(c)
		require.True(t, ok)
		c.JSON(http.StatusOK, gin.H{"user_id": id})
	})

	t.Run("no token is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me", nil))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("valid token is accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"user_id":1`)
	})

	t.Run("refresh token is rejected as an access token", func(t *testing.T) {
		refresh, _, err := issuer.Issue(
			db.CoreUser{ID: 1, UUID: ptr("u-1")},
			db.CoreUserRole{ID: domain.RoleAdmin, RoleLevel: domain.RoleAdmin},
			auth.TokenRefresh,
		)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer "+refresh)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
```

That third subtest is the security regression test for the chapter 07 exercise. It will fail the moment someone removes the token-type check — which is the entire point of writing it.

---

## 9.6 Integration tests against a real Postgres

Unit tests with fakes prove your logic. They cannot prove your SQL works. sqlc-generated code is type-checked against the schema, but semantics (soft-delete filters, `LIMIT/OFFSET`, `RETURNING`) are only verified by running against Postgres.

Guard integration tests with a **build tag** so `go test ./...` stays fast and Docker-free:

```go
//go:build integration

package repository_test
```

🧩 `internal/repository/main_test.go`:

```go
//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupPostgres starts a throwaway Postgres, applies migrations,
// and cleans everything up when the test finishes.
func setupPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("learn101_test"),
		postgres.WithUsername("learn101"),
		postgres.WithPassword("learn101"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2). // once for init, once for "ready"
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")

	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "connection string")

	// Same migrations as production — this is what keeps tests honest.
	m, err := migrate.New("file://../../db/migrations", dsn)
	require.NoError(t, err, "migrate source")
	require.NoError(t, m.Up(), "apply migrations")

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "create pool")
	t.Cleanup(pool.Close)

	return pool
}

// truncate clears mutable tables between tests, keeping seed data intact
// only when you want it. Here we clear everything user-created.
func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE core_user RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}
```

🧩 A test using it:

```go
//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"learn101/internal/db"
	"learn101/internal/domain"
	"learn101/internal/repository"
)

func TestUserRepository_SoftDelete(t *testing.T) {
	pool := setupPostgres(t)
	truncate(t, pool)

	repo := repository.NewUserRepository(pool)
	ctx := context.Background()

	created, err := repo.Create(ctx, db.CreateUserParams{
		RoleID:   domain.RoleUser,
		Name:     "Test User",
		Email:    "test@example.com",
		Password: "hashed",
		Status:   "ACTIVE",
	})
	require.NoError(t, err)
	require.NotNil(t, created.UUID)

	// Visible before deletion.
	_, err = repo.ByUUID(ctx, *created.UUID)
	require.NoError(t, err)

	// Soft delete.
	require.NoError(t, repo.SoftDelete(ctx, *created.UUID))

	// Hidden from the read path...
	_, err = repo.ByUUID(ctx, *created.UUID)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	// ...but still physically present.
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM core_user WHERE uuid = $1`, *created.UUID).Scan(&count))
	assert.Equal(t, 1, count)

	// Deleting again is a 404, not a silent success.
	err = repo.SoftDelete(ctx, *created.UUID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
```

That test verifies something no fake can: that `deleted_at IS NULL` is actually in your query, that the row survives, and that `:execrows` correctly reports zero matched rows. Run it with:

```bash
$ go test -tags=integration ./... -race
```

Add a `make test-integration` target for it. In CI, run unit tests on every push and integration tests on pull requests.

### Alternatively: a Compose-managed test database

If testcontainers feels heavy or your CI lacks Docker-in-Docker, point tests at the Compose Postgres from chapter 04 with a separate database name, and skip when the env var is absent:

```go
func setupPostgres(t *testing.T) *pgxpool.Pool {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	// ...
}
```

`t.Skip` is the idiomatic "this test is not applicable here" — better than a build tag when the distinction is environmental rather than structural.

---

## 9.7 What to test, by layer

| Layer | Test type | What it proves | Speed |
|---|---|---|---|
| `domain` | pure unit | error semantics, pure helpers | µs |
| `auth` (bcrypt, JWT) | unit | hashing round-trips, token type enforcement, expiry, tampering | µs |
| `service` | unit + fakes | business rules, normalization, authorization decisions, error mapping | µs |
| `middleware` | unit + `httptest` | auth/role gates, request IDs | µs |
| `handler` | `httptest` | status codes, envelope shape, validation messages | µs |
| `repository` | integration | SQL correctness, soft-delete filters, transactions | seconds |
| whole app | integration | `app.Build` + `httptest` — a smoke test through every layer | seconds |

The 80/20: **a lot of service and middleware unit tests, a handful of handler tests, and a few focused repository integration tests.** Don't chase 100% coverage; chase coverage of decision points (every `if err != nil` branch that returns something different).

Useful:

```bash
$ go test -cover ./internal/service/...      # per-package coverage
$ go tool cover -func=coverage.out | tail -1 # total
```

---

## 9.8 Exercises

1. Convert the `TestRequireRole` table above into real code and run it. Confirm the failing case names are readable.
2. Write `FakeUserStore` fully — all eight methods. Then write service tests for: duplicate email, email normalization, missing user on update, and soft-delete of a missing user.
3. Add the "refresh token rejected as access token" subtest from §9.5, then **deliberately delete the token-type check** from `RequireAuth` and watch the test fail. Restore it.
4. Write a handler test asserting that a `404` from the service produces `{"error":{"code":"NOT_FOUND"}}` and that no internal detail leaks.
5. Add the testcontainers integration test and run `go test -tags=integration ./internal/repository/... -v`.
6. Run `go test -race ./...` and confirm clean. Then introduce a data race deliberately (write to a package-level map from two goroutines driven by two parallel handler calls) and confirm the detector reports it.
7. Add `-coverprofile` and open the HTML report. Find your lowest-covered function and decide whether the gap matters.

## 9.9 Done when

- [ ] `go test ./... -race` passes and includes service, middleware and handler tests.
- [ ] You have written a fake by hand and never installed a mocking library.
- [ ] At least one security-relevant regression test exists (refresh-token-as-access-token).
- [ ] `go test -tags=integration ./...` starts a container and passes.
- [ ] You know which layer each test belongs to without checking §9.7.

Next: [Chapter 10 — Docker from Zero](10-docker-from-zero.md).
