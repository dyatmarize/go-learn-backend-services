# Chapter 04 — PostgreSQL, Docker and Migrations

> **Goal:** stand up PostgreSQL locally, take control of the schema that's already half-written in this repo, and connect a pool from Go. By the end, `migrate up` creates your tables and the app pings the database successfully.

This chapter is the Go-ified version of: Spring Boot's `DataSource` autoconfiguration + HikariCP + Flyway.

---

## 4.1 Where you are right now

The repo has a schema file on disk at `../../database`. It's currently **deleted from the last commit**, so treat it as in-progress work you're about to adopt properly.

Two things to fix before building on it:

**1. Move it where the rest of the tooling expects it.** sqlc will read your schema from the migrations directory, and every Go project in the wild uses `db/`. Pick one location and keep everything there:

```bash
$ mkdir -p db/migrations db/queries
$ git mv database/migrations/000001_initial_schema.up.sql db/migrations/
$ rmdir database/migrations database 2>/dev/null || true
```

**2. It has no `down` migration.** golang-migrate is strict about this — every `up` wants a matching `down` for rollbacks. We'll write it below.

---

## 4.2 PostgreSQL in Docker

Spring Boot devs usually have a local Postgres or Testcontainers. Docker Compose is the same idea and gives you a disposable, versioned database.

🧩 `docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    container_name: learn101-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: learn101
      POSTGRES_PASSWORD: learn101
      POSTGRES_DB: learn101
      # Deterministic collation makes text sorting stable across machines.
      POSTGRES_INITDB_ARGS: "--encoding=UTF8 --locale=C"
    ports:
      - "5432:5432"
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U learn101 -d learn101"]
      interval: 5s
      timeout: 5s
      retries: 10
      start_period: 10s

volumes:
  postgres-data:
```

Notes:

- No `version:` key — it's obsolete in modern Compose and produces a warning.
- The **healthcheck** is what later lets the app container wait for a ready database instead of crash-looping. `pg_isready` is the right probe, not a TCP check.
- The named volume `postgres-data` survives `docker compose down`. Use `docker compose down -v` to wipe it and start clean — that's your "drop and recreate the schema" button.
- `postgres:16-alpine` is smaller and faster to pull than the Debian image. For this project there is no behavioural difference.
- The credentials here are throwaway local values, matching the `DATABASE_URL` from chapter 03. They are not secrets.

```bash
$ docker compose up -d
$ docker compose ps
$ docker compose logs -f postgres     # watch until "database system is ready to accept connections"
$ psql "postgres://learn101:learn101@localhost:5432/learn101?sslmode=disable" -c "select version();"
```

---

## 4.3 golang-migrate

| Flyway / Liquibase | golang-migrate |
|---|---|
| `V1__initial_schema.sql` | `000001_initial_schema.up.sql` + `.down.sql` |
| checksums stored in `flyway_schema_history` | version + dirty flag in `schema_migrations` |
| Java-based, embedded in the app at startup | a standalone CLI (or a library, if you prefer) |
| `flyway migrate` | `migrate -path ... -database ... up` |
| `flyway repair` | `migrate ... force <version>` |
| Java/Kotlin/XML migrations | Plain SQL only |

Your existing filename already follows the golang-migrate convention, which is why this tool is the right pick here.

### Install

```bash
# Option 1: from source (works anywhere Go is installed)
$ go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Option 2: a package manager
$ brew install golang-migrate          # macOS
$ sudo apt install migrate             # Debian/Ubuntu, if packaged
```

Verify: `migrate -version`.

### Commands you'll actually use

```bash
$ export DATABASE_URL='postgres://learn101:learn101@localhost:5432/learn101?sslmode=disable'

# Create the next pair of files (note: -seq gives 000002_..., not timestamps)
$ migrate create -ext sql -dir db/migrations -seq create_refresh_tokens

# Apply everything pending
$ migrate -path db/migrations -database "$DATABASE_URL" up

# Roll back one migration
$ migrate -path db/migrations -database "$DATABASE_URL" down 1

# Where am I?
$ migrate -path db/migrations -database "$DATABASE_URL" version

# Roll everything back (dangerous, but great for a clean slate)
$ migrate -path db/migrations -database "$DATABASE_URL" down -all

# Reapply from scratch
$ migrate -path db/migrations -database "$DATABASE_URL" redo 1
```

### The dirty flag

If a migration fails halfway (syntax error, or you kill the process), golang-migrate marks the schema **dirty** and refuses to continue. This is a feature: PostgreSQL DDL is transactional, but the migrate tool cannot always know what was committed.

```bash
$ migrate ... version
# 1 (dirty)

# Inspect the damage manually, fix the DB by hand, then:
$ migrate ... force 1
```

Two habits that keep you out of this state:

1. **Wrap multi-statement migrations in a transaction explicitly.** PostgreSQL DDL is transactional, so this works:
   ```sql
   BEGIN;
   -- statements
   COMMIT;
   ```
2. **Never edit a migration that has been applied.** Write `000002_...` instead. The version number is the contract; editing history means every machine that already ran `000001` diverges silently.

### Writing the down migration

The `down` reverses the `up` exactly, in reverse order. 🧩 `db/migrations/000001_initial_schema.down.sql`:

```sql
BEGIN;

DROP TABLE IF EXISTS core_user;
DROP TABLE IF EXISTS core_user_role;

COMMIT;
```

Child table first, then parent — that order matters as soon as you add a foreign key.

---

## 4.4 Reviewing the schema you inherited

Here's the `up` migration with the odd whitespace normalised, plus commentary:

```sql
CREATE TABLE IF NOT EXISTS core_user_role
(
    id         bigserial    constraint role_pkey primary key,
    created_at timestamp(6) with time zone,
    deleted_at timestamp(6) with time zone,
    updated_at timestamp(6) with time zone,
    role_name  varchar(255) not null,
    role_level bigint       not null
);

INSERT INTO core_user_role (created_at, deleted_at, updated_at, role_name, role_level)
VALUES
    (now(), null, now(), 'SUPER_USER', 1),
    (now(), null, now(), 'ADMIN', 2),
    (now(), null, now(), 'USER', 3)
ON CONFLICT DO NOTHING;
```

```sql
CREATE TABLE IF NOT EXISTS core_user
(
    id         bigserial    constraint core_user_pkey primary key,
    created_at timestamp(6) with time zone,
    deleted_at timestamp(6) with time zone,
    updated_at timestamp(6) with time zone,
    role_id    bigint       not null,
    name       varchar(255) not null,
    email      varchar(255) not null unique,
    password   varchar(255) not null,
    status     varchar(255) not null,
    uuid       varchar(255) default gen_random_uuid()
);

INSERT INTO core_user (created_at, updated_at, role_id, name, email, password, status)
VALUES
    (now(), now(), 1, 'dyatmarize', 'dyatmarize@cool.app', '$2a$10$w0bj4LBOgzZeH7lIoF2Wae8fuUWSu6eXFjoR6BZzOWioCLB6GvRmO', 'ACTIVE'),
    (now(), now(), 2, 'admin',      'admin@cool.app',      '$2a$10$huZLni..OVCjCl8T..f/yOdSOBlNmIW.vGeeoQvgeGyr0rUk09f9.', 'ACTIVE')
ON CONFLICT DO NOTHING;
```

Good decisions already in there:

- `bigserial` primary keys — the PostgreSQL-idiomatic auto-increment.
- `timestamp(6) with time zone` everywhere. **Always use `timestamptz`, never bare `timestamp`.** Store instants, not wall-clock.
- `deleted_at` for soft deletes, which lets you keep an audit trail and never hard-delete a user.
- `gen_random_uuid()` is built into PostgreSQL 13+ (before that you needed the `pgcrypto` extension). No extension needed on PG 16.
- Seed data *looks* idempotent, but only `core_user` genuinely is: it has a unique `email`, so `ON CONFLICT DO NOTHING` fires there. `core_user_role` has no unique key on `role_name` and the seed omits `id`, so **re-running that INSERT does create duplicate roles**. This is a real latent bug — see the table below.

Worth knowing, and good exercises (don't block on these):

| Observation | Suggestion |
|---|---|
| No foreign key from `core_user.role_id` to `core_user_role.id` | Add one in a later migration. It's free integrity you're currently hand-maintaining. |
| `uuid` stored as `varchar(255)` | The native `uuid` type is 16 bytes, indexed better, and can't hold garbage. Changing it means a `000002` migration, not editing `000001`. |
| `status` is a free-form `varchar` | A `CHECK (status IN ('ACTIVE','INACTIVE','SUSPENDED'))` or a Postgres `ENUM` removes a whole class of bugs. |
| `role_level` privileges run **backwards** to intuition | `SUPER_USER=1` is the *most* privileged. Your authorization checks must be `role_level <= required`, not `>=`. |
| Seed users are inserted by a schema migration | Fine for a learning project. In production, seed via a separate job so schema and data lifecycles don't mix. |
| `core_user_role` has no unique key, so its seed INSERT re-inserts duplicate roles on every run | Add `UNIQUE (role_name)` in a `000002` migration; the `ON CONFLICT DO NOTHING` then starts doing what it looks like it does. |
| `email` is `unique` while soft deletes are in play | A plain unique index blocks re-registering a soft-deleted email. A partial index fixes it: `CREATE UNIQUE INDEX ON core_user (email) WHERE deleted_at IS NULL;` |
| Only two seed users, and their bcrypt hashes have **unknown plaintext** | Nobody knows those passwords. You'll create a user with a known password in chapter 07. |

---

## 4.5 Connecting from Go with `pgxpool`

`pgx` is the Go-native PostgreSQL driver. Its pool (`pgxpool`) replaces HikariCP.

| HikariCP | pgxpool |
|---|---|
| `maximumPoolSize` | `MaxConns` |
| `minimumIdle` | `MinConns` |
| `maxLifetime` | `MaxConnLifetime` |
| `idleTimeout` | `MaxConnIdleTime` |
| `connectionTimeout` | context timeout on `pgxpool.New` / `Ping` |
| `dataSource.getConnection()` | `pool.Acquire(ctx)` (rarely needed — most calls take the pool) |
| Spring-managed lifecycle | `defer pool.Close()` in `main` |

🧩 `internal/dbpool/pool.go`:

```go
package dbpool

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"learn101/internal/config"
)

// New builds a connection pool and verifies it can reach the database.
// The caller owns the returned pool and must Close it.
func New(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	// Pool sizing: modest by default. Postgres defaults to 100 max connections,
	// and every replica of your service multiplies the demand.
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	// Identify this application in pg_stat_activity, so "which service is
	// holding that lock?" has an answer.
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "learn101-api"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Fail fast: prove the credentials and network work before serving traffic.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
```

Wiring it into `main` (which now needs to own the pool's lifetime):

```go
func run() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not load .env", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := dbpool.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	logger.Info("connected to database", "addr", cfg.HTTPAddr)
	return nil
}
```

`signal.NotifyContext` appears early on purpose — it's how a `Ctrl-C` will later cancel in-flight queries and trigger graceful shutdown (chapter 11).

### Why not `../../database`?

`../../database` is the standard interface, and `pgx` can plug into it via `stdlib`. But the native `pgx` API gives you PostgreSQL-specific features that `../../database` abstracts away: `jsonb` handling, `COPY` protocol, typed arrays, and `pgx.Row` scanning without reflection. Since you know you're on PostgreSQL, use `pgx` directly — sqlc generates code for the native API too.

---

## 4.6 Migration workflow that scales

For a solo weekend project, this is enough. For anything longer-lived:

1. **One concern per migration.** `add_users_table`, then `add_role_fk`, then `add_status_check`. Never "assorted changes".
2. **Migrations are append-only.** Once applied anywhere other than your laptop, they're immutable.
3. **Run migrations as a separate step, not inside the app boot.** If you migrate on startup, every replica races to migrate, and a failure takes down the service rather than failing a deploy. Flyway-in-Spring does this by default and it's a known footgun; here you get to avoid it:

   ```bash
   $ migrate -path db/migrations -database "$DATABASE_URL" up && ./bin/api
   ```

4. **Test your `down` migrations.** Roll one back, roll it forward again. A `down` that has never run is a `down` that doesn't work.
5. **Backfill data in separate `up` migrations** from the DDL change that enables them, so a long backfill doesn't hold an exclusive lock.

---

## 4.7 Exercises

1. Bring up Postgres and confirm `000001` applies: `migrate ... up`, then `psql ... -c "\dt"` and `-c "select id, role_name, role_level from core_user_role order by role_level;"`.
2. Roll back with `migrate ... down 1` and confirm the tables are gone. Roll forward again. This is the fastest way to trust your `down`.
3. **Make it dirty on purpose.** Insert a deliberate syntax error into a copy of `000001`, run `up`, read the error, observe `version` reporting `dirty`, and recover with `force 1`. Do this now while nothing matters.
4. `docker compose down -v && docker compose up -d && migrate ... up` — prove the whole cycle is reproducible from nothing.
5. Write `000002_add_role_fk.up.sql` adding the foreign key plus a partial unique index on `email`, and the matching `down`. Confirm `migrate ... up` then `down 1` then `up` all succeed.
6. Kill Postgres (`docker compose stop postgres`) and start the app. Confirm you get a clear `ping database` error rather than a hang.

## 4.8 Done when

- [ ] `docker compose up -d` shows `postgres` healthy.
- [ ] `migrate ... version` reports `1` (clean, not dirty).
- [ ] `\dt` in `psql` lists `core_user`, `core_user_role`, `schema_migrations`.
- [ ] `select count(*) from core_user;` returns 2.
- [ ] The app starts, logs `connected to database`, and exits cleanly on `Ctrl-C`.
- [ ] `000001_initial_schema.down.sql` exists, and you have personally run it.

Next: [Chapter 05 — sqlc and Data Access](05-sqlc-and-data-access.md).
