# Learning Guide 101 — Building Backend Services in Go

A hands-on guide for a **Java / Spring Boot developer** building a REST API with **Go + PostgreSQL**.

You already know how to design a backend. What you don't yet know is how Go expresses the same ideas — and Go deliberately refuses to give you most of the machinery Spring Boot hands you for free. This guide is about that translation layer.

---

## What this guide is

- A **chapter-by-chapter build** of a real REST API over the schema already sitting in `../../database`.
- A **translation table** at every step: "in Spring Boot you'd reach for X; in Go you write Y."
- **Complete, runnable code**. Every snippet is meant to be typed, not skimmed.

## What this guide is not

- Not a Go language reference. It teaches the subset you need to ship a service.
- Not a survey of every framework. It picks one stack deliberately and explains why.
- Not production-complete. Auth tokens, error taxonomy, and tests are real, but things like rate limiting, tracing backends, and deployment pipelines are named and left as exercises.

## How to read this

1. Read a chapter once, without typing.
2. Read it again with your editor open and **type every snippet yourself**. Do not copy-paste — the typing is where the syntax sticks.
3. Run the "Done when" check at the end of each chapter before moving on.
4. When you hit something that feels wrong compared to Java, check [Chapter 12](12-spring-boot-to-go-cheatsheet.md) — it's probably covered there.

## Prerequisites

| Tool | Why | Check |
|---|---|---|
| Go 1.27+ | The `go.mod` in this repo targets 1.27 | `go version` |
| Docker + Compose | Runs PostgreSQL locally | `docker compose version` |
| `psql` | Inspecting your data directly | `psql --version` |
| [golang-migrate](https://github.com/golang-migrate/migrate) CLI | Applies migrations | `migrate -version` |
| [sqlc](https://sqlc.dev) CLI | Generates typed Go from SQL | `sqlc version` |
| GoLand (you already have `.idea/` committed) | IDE | — |

The repo's current state, for reference:

```
go.mod                 module learn101, go 1.27 — a single import (godotenv), no go.sum yet
main.go                7 lines: calls godotenv.Load(".env") and ignores the error
.env                   present but empty
database/migrations/   000001_initial_schema.up.sql  (no .down.sql)
```

## The mental model shift

This table is the single most important thing in the guide. Everything else is detail.

| Spring Boot | Go | Notes |
|---|---|---|
| Framework owns your app | **You own `main()`** | Go has no application container. `func main()` starts, wires, and runs everything explicitly. |
| `@SpringBootApplication` | `cmd/api/main.go` | Nothing scans, nothing is discovered. |
| DI container (`@Autowired`) | **Constructor injection, by hand** | Dependencies are struct fields passed into a `NewXxx()` function. |
| Annotations | **Plain code** | There is no annotation processing. Routing, validation and transactions are function calls and struct tags. |
| `@RestController` | Handler struct + registered routes | Routes are registered once, explicitly, at startup. |
| `@Service` / `@Repository` | Just a struct | Stereotypes don't exist. A "service" is a struct with methods. |
| JPA / Hibernate | **Hand-written SQL** (via sqlc) | No lazy loading, no dirty checking, no session. You write the SQL you mean. |
| Flyway / Liquibase | golang-migrate | Same idea, same file conventions. |
| HikariCP | `pgxpool` | Pooled connections, built into the driver. |
| `@Transactional` | **Explicit `pgx.Tx`** | No proxy magic. You beg, commit, and roll back yourself. |
| Exceptions | **`error` return values** | Errors are ordinary values. `if err != nil` is everywhere by design. |
| `interface` + `implements` | **Implicit interfaces** | A type satisfies an interface by having the methods. No declaration needed. |
| Maven `pom.xml` | `go.mod` + `go.sum` | Much smaller. No transitive version conflicts to resolve. |
| `application.yml` | Env vars into a struct | Config is just a struct you fill and validate. |
| SLF4J / Logback | stdlib `log/slog` | Structured logging in the standard library. |
| Actuator | Hand-written `/healthz`, `/readyz` | Small enough to write yourself. |

## The stack this guide builds on

| Concern | Choice | Why this and not the JPA-equivalent |
|---|---|---|
| HTTP | **Gin** (`gin-gonic/gin`) | Most popular Go web framework; closest map from Spring MVC. Chapter 06 also shows the `chi` and stdlib `net/http` equivalents — an Android client cannot tell the difference. |
| Driver | **pgx v5** (`jackc/pgx/v5`) | The Go-native PostgreSQL driver, with a built-in pool. |
| Data access | **sqlc** | You write SQL; it generates typed Go functions and structs. Compile-time checked, no ORM runtime. |
| Migrations | **golang-migrate** | Matches the `000001_initial_schema.up.sql` naming already in this repo. |
| Auth | **`golang-jwt/jwt/v5`** + `golang.org/x/crypto/bcrypt` | The bcrypt hashes are already seeded in your migration. |
| Validation | **`go-playground/validator/v10`** | What Gin's `binding:"..."` tags use underneath. |
| Logging | **`log/slog`** | Standard library, JSON output, no dependency. |

## The API you will end up with

Built against the existing `core_user` / `core_user_role` tables.

```
POST   /api/v1/auth/login          -> access + refresh token
POST   /api/v1/auth/refresh        -> new access token

GET    /api/v1/users/me            -> current user          (any authenticated)
GET    /api/v1/users               -> paginated list        (ADMIN, SUPER_USER)
POST   /api/v1/users               -> create user           (ADMIN, SUPER_USER)
GET    /api/v1/users/:uuid         -> fetch one             (ADMIN, SUPER_USER)
PATCH  /api/v1/users/:uuid         -> update                (ADMIN, SUPER_USER)
DELETE /api/v1/users/:uuid         -> soft delete           (SUPER_USER)

GET    /api/v1/roles               -> list roles            (authenticated)

GET    /healthz                    -> liveness
GET    /readyz                     -> readiness (pings the DB)
```

Roles come from `core_user_role`, ordered by `role_level`: `SUPER_USER(1) < ADMIN(2) < USER(3)` — **lower level means more privilege**, which trips people up. That's your schema, so that's what we use. Add a `role_level <= n` check and you have RBAC.

## Roadmap

Seven milestones, M0 through M6. Each is a chapter cluster, and each ends with something you can actually run.

| # | Milestone | Chapters | Done when |
|---|---|---|---|
| **M0** | Bootstrap | 01, 02 | `go build ./...` succeeds and `go mod tidy` produces a `go.sum`. |
| **M1** | Config | 03 | The app refuses to start with a missing `DATABASE_URL`, and starts cleanly with one. |
| **M2** | Database | 04 | `docker compose up -d` + `migrate up` creates `core_user` and `core_user_role`, and the app pings the pool successfully. |
| **M3** | Data access | 05 | `sqlc generate` produces `internal/db`, and a query returns your seeded `dyatmarize` row. |
| **M4** | HTTP CRUD | 06 | `curl` can list, fetch and create users against a running server. |
| **M5** | Auth | 07, 08 | `curl` login returns a JWT, and a protected route rejects a request without one. |
| **M6** | Test + ship | 09, 10, 11 | `go test ./... -race` passes and the API runs in Docker Compose. |

## Chapters

| Chapter | Topic |
|---|---|
| [01](01-go-language-for-java-devs.md) | Go for Java developers — the language gap |
| [02](02-modules-layout-and-tooling.md) | Modules, project layout, tooling |
| [03](03-configuration.md) | Configuration and secrets |
| [04](04-postgres-docker-and-migrations.md) | PostgreSQL, Docker, migrations |
| [05](05-sqlc-and-data-access.md) | sqlc and data access |
| [06](06-http-api-with-gin.md) | The HTTP layer with Gin |
| [07](07-auth-jwt-and-rbac.md) | Authentication, JWT and RBAC |
| [08](08-errors-logging-and-observability.md) | Errors, logging, observability |
| [09](09-testing.md) | Testing |
| [10](10-docker-from-zero.md) | Docker from zero — images, containers, Compose |
| [11](11-docker-and-graceful-shutdown.md) | Docker in practice — the image, Compose, graceful shutdown |
| [12](12-spring-boot-to-go-cheatsheet.md) | Spring Boot → Go cheat sheet |
| [13](13-roadmap-and-next-steps.md) | Exercises and what to learn next |

## Conventions used in this guide

- **Paths in code blocks are relative to the repository root** (`learn101/`), not to this `docs/` folder. So `internal/config/config.go` means `learn101/internal/config/config.go`.
- `$` at the start of a line means "run this in your shell".
- Snippets marked 🧩 are complete files you can paste in and compile.
- Snippets marked 💡 are excerpts or illustrations — they may not compile standalone.

## A note on this folder

Everything here is Markdown. Go only compiles directories that contain `.go` files, so `docs/` is invisible to `go build ./...`, `go vet ./...` and `go mod tidy`. Putting documentation here is safe — just don't drop a `.go` file inside it, or it becomes a package the toolchain will try to build.

Onward to [Chapter 01](01-go-language-for-java-devs.md).
