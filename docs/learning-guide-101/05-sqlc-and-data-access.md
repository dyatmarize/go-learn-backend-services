# Chapter 05 — sqlc and Data Access

> **Goal:** stop thinking in entities and repositories, start thinking in SQL and typed functions. By the end, `sqlc generate` produces a package you call from Go, and the compiler catches your typos in queries before you run anything.

This is the chapter where the JPA reflex hurts most. Read it twice.

---

## 5.1 Why not an ORM

Spring Boot gives you JPA/Hibernate, and it feels productive: annotate a class, get CRUD. Go has ORM-ish libraries (GORM, ent) and plenty of teams use them. This guide deliberately doesn't, for reasons that map directly onto pain you've already experienced with Hibernate:

| Hibernate behaviour | What it costs you | sqlc's answer |
|---|---|---|
| Lazy loading | `LazyInitializationException`, N+1 queries discovered in production | No lazy loading exists. One query returns exactly the columns you selected. |
| Dirty checking / flush timing | Objects change without you calling `save()`; `flush()` ordering surprises | Nothing happens implicitly. You call an explicit UPDATE. |
| `@Transactional` proxy | Self-invocation breaks it; rollback rules are subtle | `pgx.Tx` is a value you hold. If you didn't call `Commit`, it rolled back. |
| Criteria API / derived queries | `findByEmailAndStatusNotAndDeletedAtIsNull` | Write the SQL. It's right there, readable. |
| N+1 via `@OneToMany` | Fixed with `JOIN FETCH`, which you discover late | A `JOIN` in the query. Or two queries you consciously chose. |
| Schema drift vs entities | `ddl-auto=validate` failing at startup | The SQL in `db/queries` is checked against your migrations by sqlc. |
| Session/cache semantics | First-level cache means stale reads | Every call is a fresh round trip. No cache to reason about. |

The pattern people land on in Go is: **you write SQL, a generator writes the boring Go around it.** You keep full control of the query, and you get compile-time safety in exchange for a codegen step.

sqlc generates **pure Go functions and structs** from your `.sql` files. There is no runtime library, no reflection, no session. `sqlc generate` is exactly like `protoc` or a MapStruct/annotation-processor step: source in, source out.

---

## 5.2 Install

```bash
# Option 1: Go install
$ go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# Option 2: package manager
$ brew install sqlc
$ sudo snap install sqlc

$ sqlc version
```

---

## 5.3 Configuration

🧩 `sqlc.yaml` at the repo root:

```yaml
version: "2"
sql:
  - engine: "postgresql"
    # sqlc reads your schema from the migrations directory.
    # It understands golang-migrate naming and ignores *.down.sql.
    schema: "db/migrations"
    queries: "db/queries"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"          # generate against pgx, not database/sql
        emit_json_tags: true
        emit_interface: true           # produces Querier — great for testing
        emit_empty_slices: true        # return []T{} instead of nil
        emit_pointers_for_null_types: false
        overrides:
          # Nullable timestamptz -> *time.Time instead of pgtype.Timestamptz.
          # Verbose but usable: you compare with nil, like Java's Instant.
          - db_type: "timestamptz"
            nullable: true
            go_type:
              import: "time"
              type: "Time"
              pointer: true
```

A note on `out: "internal/db"`: generated code lives under `internal/`, so nothing outside your module can import it. You regenerate this package; you never hand-edit it. If you're using git, it's reasonable to commit the generated files so builds don't require the sqlc binary — just add a header comment policy: **never edit `internal/db/*.sql.go`.**

---

## 5.4 Writing queries

sqlc queries are plain SQL with a **name annotation** and a **return-shape annotation**.

```sql
-- name: GetUserByEmail :one
```

The four shapes you'll use:

| Annotation | Returns | Use when |
|---|---|---|
| `:one` | a single struct, or `pgx.ErrNoRows` | fetch by unique key |
| `:many` | `[]T` | lists |
| `:exec` | just `error` | statements with no return |
| `:execrows` | `(int64, error)` — rows affected | you need to know if anything matched |
| `:execresult` | `pgconn.CommandTag` | you want the raw tag |

### 🧩 `db/queries/user.sql`

```sql
-- name: GetUserByID :one
SELECT *
FROM core_user
WHERE id = $1
  AND deleted_at IS NULL;

-- name: GetUserByUUID :one
SELECT *
FROM core_user
WHERE uuid = $1
  AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT *
FROM core_user
WHERE email = $1
  AND deleted_at IS NULL;

-- name: ListUsers :many
SELECT *
FROM core_user
WHERE deleted_at IS NULL
ORDER BY id
LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT count(*)
FROM core_user
WHERE deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO core_user (role_id, name, email, password, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateUser :one
UPDATE core_user
SET name       = $2,
    email      = $3,
    role_id    = $4,
    status     = $5,
    updated_at = now()
WHERE uuid = $1
  AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUser :execrows
UPDATE core_user
SET deleted_at = now(),
    updated_at = now()
WHERE uuid = $1
  AND deleted_at IS NULL;
```

### 🧩 `db/queries/role.sql`

```sql
-- name: ListRoles :many
SELECT *
FROM core_user_role
WHERE deleted_at IS NULL
ORDER BY role_level;

-- name: GetRoleByID :one
SELECT *
FROM core_user_role
WHERE id = $1
  AND deleted_at IS NULL;
```

### Points worth internalizing

**`RETURNING *` is your `save()`.** Postgres returns the inserted/updated row in one round trip. No `saveAndFlush`, no re-select, no stale entity. Note it also gives you server-generated `id`, `created_at` and `uuid` for free.

**Soft delete is just a `WHERE` clause.** There is no `@Where(clause = "deleted_at is null")` — you write it every time, which is more typing and far less magic. The tradeoff is explicit and reviewable.

**`updated_at` does not update itself.** JPA has `@PreUpdate`; sqlc has no lifecycle hooks by design. Two options:

```sql
-- Option A: set it in every UPDATE (as above — simple, visible)
-- Option B: a database trigger, defined once in a migration
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER core_user_set_updated_at
    BEFORE UPDATE ON core_user
    FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
```

Option B is the better long-term answer because it can't be forgotten. Either way, the logic lives in SQL, not in Go reflection.

**`defer` has no analogue.** `deleted_at IS NULL` filtering means a hard delete never happens. If you ever need to purge, that's a separate, deliberate statement.

### Optional filters

sqlc requires static SQL — no dynamic `WHERE` building. For optional filters, use `sqlc.narg` (nullable argument) with a `COALESCE`-style guard:

```sql
-- name: SearchUsers :many
SELECT *
FROM core_user
WHERE deleted_at IS NULL
  AND (sqlc.narg('q')::text IS NULL OR name ILIKE '%' || sqlc.narg('q')::text || '%')
  AND (sqlc.narg('role_id')::bigint IS NULL OR role_id = sqlc.narg('role_id')::bigint)
ORDER BY id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
```

The explicit `::text` / `::bigint` casts are required so Postgres can infer parameter types. This is the price of static SQL, and it's a fair one: your filter logic is visible and testable, not assembled by a Criteria builder at runtime.

---

## 5.5 Running the generator

```bash
$ sqlc generate
```

Then read what you got:

```bash
$ tree internal/db
internal/db
├── db.go          # New(DBTX) *Queries, Queries.WithTx
├── models.go      # CoreUser, CoreUserRole structs
├── querier.go     # the Querier interface (because emit_interface: true)
├── role.sql.go
└── user.sql.go
```

💡 The generated model for your table looks like this:

```go
type CoreUser struct {
	ID        int64      `json:"id"`
	CreatedAt *time.Time `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	RoleID    int64      `json:"role_id"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	Password  string     `json:"password"`
	Status    string     `json:"status"`
	UUID      *string    `json:"uuid"`
}
```

Note `CreatedAt` is `*time.Time` because the column is nullable and you set the override — good, since your migration doesn't set a `DEFAULT now()`. `UUID` is `*string` for the same reason. Both reflect the schema honestly; that's the point.

💡 And a generated method:

```go
const getUserByEmail = `-- name: GetUserByEmail :one
SELECT *
FROM core_user
WHERE email = $1
  AND deleted_at IS NULL
`

func (q *Queries) GetUserByEmail(ctx context.Context, email string) (CoreUser, error) {
	row := q.db.QueryRow(ctx, getUserByEmail, email)
	var i CoreUser
	err := row.Scan(
		&i.ID, &i.CreatedAt, &i.DeletedAt, &i.UpdatedAt,
		&i.RoleID, &i.Name, &i.Email, &i.Password, &i.Status, &i.UUID,
	)
	return i, err
}
```

That's it — no magic. You could have written it by hand; sqlc just refuses to let it drift.

**A word of warning on `SELECT *`**: convenient while learning, brittle in production. Adding a column changes the generated struct and every `Scan` call. Once the schema stabilises, switch to explicit column lists. Also, `SELECT *` here returns `password` — you never want that in an API response, which is exactly why chapter 06 uses a separate DTO rather than exposing `db.CoreUser`.

---

## 5.6 The repository layer

`internal/db` is generated and speaks in database terms. Your services shouldn't see `pgx.ErrNoRows` or sqlc parameter structs. So you wrap it.

🧩 `internal/repository/user_repository.go`:

```go
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"learn101/internal/db"
	"learn101/internal/domain"
)

type UserRepository struct {
	q    *db.Queries
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{
		q:    db.New(pool),
		pool: pool,
	}
}

func (r *UserRepository) ByEmail(ctx context.Context, email string) (db.CoreUser, error) {
	u, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

func (r *UserRepository) ByUUID(ctx context.Context, uuid string) (db.CoreUser, error) {
	u, err := r.q.GetUserByUUID(ctx, uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by uuid: %w", err)
	}
	return u, nil
}

func (r *UserRepository) List(ctx context.Context, limit, offset int32) ([]db.CoreUser, error) {
	users, err := r.q.ListUsers(ctx, db.ListUsersParams{Limit: limit, Offset: offset})
	if err != nil {
		// Lists "succeed" with zero rows; ErrNoRows is not expected here.
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (r *UserRepository) Create(ctx context.Context, p db.CreateUserParams) (db.CoreUser, error) {
	u, err := r.q.CreateUser(ctx, p)
	if err != nil {
		return db.CoreUser{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (r *UserRepository) SoftDelete(ctx context.Context, uuid string) error {
	rows, err := r.q.SoftDeleteUser(ctx, uuid)
	if err != nil {
		return fmt.Errorf("soft delete user: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}
```

Two translation duties live here and nowhere else:

- **`pgx.ErrNoRows` → `domain.ErrNotFound`.** Above this layer, nobody knows what driver you use.
- **`rows == 0` → `domain.ErrNotFound`.** An `:execrows` that matched nothing is a 404, not a success.

Also notice `db.ListUsersParams` — sqlc generates a params struct for multi-argument queries instead of positional args, which is far more readable than `ListUsers(ctx, 10, 0)`.

### Interface for testability

Define the interface where it's **used** (chapter 01), i.e. in the service package:

💡 `internal/service/user_service.go` (excerpt):

```go
// UserStore is everything the service layer needs from persistence.
// Named for behaviour, not for the implementation that provides it.
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

`*repository.UserRepository` satisfies it as soon as you add `ByID`, `Count` and `Update` in the same shape as the methods in §5.6 — and the handler tests in chapter 09 depend on exactly this shape, so keep the two in sync. No annotation, no `@Repository` — and a hand-written fake satisfies it too.

---

## 5.7 Transactions

`@Transactional` doesn't exist. You hold a `pgx.Tx` and commit it yourself.

sqlc generates `Queries.WithTx(tx)` so the same generated methods run inside the transaction:

```go
func (r *UserRepository) createWithAudit(ctx context.Context, p db.CreateUserParams) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a no-op after Commit returns successfully.
	// This defer is what guarantees the connection is released.
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.q.WithTx(tx)

	user, err := qtx.CreateUser(ctx, p)
	if err != nil {
		return fmt.Errorf("create user: %w", err)  // deferred rollback fires
	}

	if _, err := qtx.InsertAuditLog(ctx, user.ID); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
```

Compare directly:

| Spring | Go |
|---|---|
| `@Transactional` on the method | `pool.Begin(ctx)` inside the method |
| proxy intercepts the call | you call `Begin` explicitly |
| rollback on unchecked exception | rollback on any returned error — you choose |
| self-invocation bypasses the proxy | no proxies, no bypass; it's just code |
| `TransactionTemplate` | a helper function taking `func(q *db.Queries) error` |
| `Propagation.REQUIRED` etc. | you pass context and decide; nested transactions are rare by design |

**Isolation level:** `tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})` if you need it. Default is `ReadCommitted`, same as Postgres' default.

### A helper worth having

Because the begin/defer-rollback/commit dance is boilerplate, wrap it. Put it in `internal/dbpool` next to the pool construction:

🧩 `internal/dbpool/tx.go`:

```go
package dbpool

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"learn101/internal/db"
)

// WithTx runs fn inside a transaction, committing on success and
// rolling back on any error or panic.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(q *db.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

Usage reads almost like Spring:

```go
err := dbpool.WithTx(ctx, r.pool, func(q *db.Queries) error {
	user, err := q.CreateUser(ctx, p)
	if err != nil {
		return err
	}
	return q.InsertAuditLog(ctx, user.ID)
})
```

This is as close as Go gets to `@Transactional` — and it's ~15 lines you can debug.

---

## 5.8 Where sqlc stops, and what to do about it

| Need | sqlc alone | What you add |
|---|---|---|
| Multi-statement business rules | not its job | `internal/service`, wrapped in `WithTx` |
| Caching | no | explicit cache in the service, or none |
| Auditing | no hooks | a Postgres trigger, or an explicit insert in the same transaction |
| Migrations | reads them, doesn't run them | golang-migrate CLI (chapter 04) |
| Complex dynamic search | `sqlc.narg` only | raw SQL via `pool.Query` for the 1% that needs it |
| Aggregates across 3+ joins | fine, it's SQL | — |

The escape hatch matters: for a genuinely gnarly query, you can always call `pool.Query(ctx, sql, args...)` directly and scan by hand. sqlc is a convenience, not a cage. Don't contort a query to satisfy the generator.

---

## 5.9 Exercises

1. Run `sqlc generate`, then open `internal/db/user.sql.go` and read `ListUsers` end to end. Confirm the params struct and the `LIMIT/OFFSET` ordering match your SQL.
2. Write `CountUsers` usage into a small `main`-adjacent test or a temporary command that prints the user count. Confirm it returns `2` (your seeded rows).
3. Add a `GetUserByStatus :many` query and use it. Note that `status` is a plain string — you get no enum safety (see chapter 04's `CHECK` suggestion).
4. **Break it on purpose.** Rename the `email` column to `email_address` in a scratch copy of the query file and run `sqlc generate`. Read the error message. This is the compile-time safety you're paying the codegen step for.
5. Add `SoftDeleteUser` + `GetUserByUUID` calls in a throwaway test, run them against your local DB, and confirm the row disappears from `GetUserByUUID` but still exists in `psql` with `deleted_at` set.
6. Implement the `WithTx` helper and use it to create a user and insert an audit row atomically. Then make the second insert fail deliberately (e.g. reference a nonexistent table) and verify the user was **not** created.
7. Add a `SearchUsers` query using `sqlc.narg` so that a blank `q` returns everything. This is your pagination/search foundation for chapter 06.

## 5.10 Done when

- [ ] `sqlc generate` succeeds and `internal/db` contains models, queries and a `Querier`.
- [ ] You can name the four return-shape annotations from memory.
- [ ] `pgx.ErrNoRows` is translated to `domain.ErrNotFound` in exactly one layer.
- [ ] You have run a transaction that rolled back and verified the side effect was undone.
- [ ] You can explain why there is no lazy loading and why that's a feature.

Next: [Chapter 06 — The HTTP Layer with Gin](06-http-api-with-gin.md).
