# Chapter 13 — Exercises and What's Next

> **Goal:** consolidate what you've built, then keep going. This chapter is the build order, the capstone exercises, and the reading list.

---

## 13.1 Build order

Work in this order. Each milestone ends with something runnable, and each depends only on the ones before it.

### M0 — Bootstrap
**Chapters:** [01](01-go-language-for-java-devs.md), [02](02-modules-layout-and-tooling.md)

```bash
$ go mod tidy
$ mkdir -p cmd/api internal/{app,config,dbpool,domain,repository,service,handler,middleware,auth,logging}
# apply the .gitignore from chapter 02, then:
$ git rm --cached learn101
$ go build ./...
```

**Done when:** `go build ./...` succeeds, `go.sum` exists, the compiled binary is untracked, `gofmt -l .` is silent.

---

### M1 — Configuration
**Chapter:** [03](03-configuration.md)

Fill in `.env`, commit `.env.example`, write `internal/config/config.go`.

**Done when:** the app exits `1` with `DATABASE_URL is required` when the variable is missing, and starts cleanly when it's set. `go test ./internal/config/...` passes.

---

### M2 — Database and migrations
**Chapter:** [04](04-postgres-docker-and-migrations.md)

```bash
$ mkdir -p db/migrations db/queries
$ git mv database/migrations/000001_initial_schema.up.sql db/migrations/
$ docker compose up -d
$ migrate -path db/migrations -database "$DATABASE_URL" up
$ psql "$DATABASE_URL" -c "select id, role_name, role_level from core_user_role order by role_level;"
```

**Done when:** `migrate ... version` reports `1` and clean; `\dt` shows your three tables; the app logs `connected to database`; you have personally run the `down` migration at least once.

---

### M3 — Data access
**Chapter:** [05](05-sqlc-and-data-access.md)

Write `sqlc.yaml`, `db/queries/user.sql`, `db/queries/role.sql`, then `sqlc generate`.

**Done when:** `internal/db` exists with models, queries and a `Querier`; `CountUsers` returns `2`; you have run a transaction that rolled back.

---

### M4 — HTTP CRUD
**Chapter:** [06](06-http-api-with-gin.md)

**Done when:** `curl` can list, fetch, create and update users; validation failures return `422` with a `fields` array; `db.CoreUser` never appears in a response.

---

### M5 — Auth
**Chapters:** [07](07-auth-jwt-and-rbac.md), [08](08-errors-logging-and-observability.md)

```bash
$ go run ./cmd/hashpw 'Password123!'
$ psql "$DATABASE_URL" -c "UPDATE core_user SET password = '<hash>' WHERE email = 'dyatmarize@cool.app';"
```

**Done when:** login returns tokens; `/users/me` requires a token; `admin` gets `403` on the SUPER_USER-only delete; every response carries `X-Request-ID`; a deliberate panic returns a clean `500`.

---

### M6 — Test and ship
**Chapters:** [09](09-testing.md), [10](10-docker-from-zero.md), [11](11-docker-and-graceful-shutdown.md)

```bash
$ go test ./... -race
$ go test -tags=integration ./... -race
$ export JWT_SECRET="$(openssl rand -base64 48)"
$ docker compose up -d --build
$ docker compose stop api     # watch the graceful drain
```

**Done when:** unit and integration tests pass; `docker compose up` brings up Postgres, migrates, and serves; `docker compose stop api` drains cleanly.

---

## 13.2 Repo hygiene checklist

Things the guide told you to change in the repo but that weren't done for you:

- [ ] `.gitignore` includes `learn101` (the binary), `.env`, `*.out`, `/bin/`
- [ ] `git rm --cached learn101` — the compiled binary is no longer tracked
- [ ] `git check-ignore -v .env` confirms `.env` is ignored
- [ ] `.env.example` is committed with placeholders and no secrets
- [ ] `go.sum` exists and is committed
- [ ] `db/migrations/000001_initial_schema.down.sql` exists
- [ ] `../../database` is gone; everything lives under `db/`
- [ ] `docs/` contains no `.go` files (so it stays out of the build)

Then, when you're ready:

```bash
$ git status
$ git add -A
$ git commit -m "feat: rest api skeleton with config, migrations, sqlc and gin"
```

---

## 13.3 Capstone exercises

Each one adds a genuinely useful capability and forces you to touch every layer. Pick them off in any order after M6.

**1. Refresh token rotation and revocation**
Add a `core_refresh_token` table (`jti`, `user_id`, `expires_at`, `revoked_at`, `replaced_by_jti`). On refresh: verify the incoming `jti` is unused, mark it revoked, issue a new pair. If a *revoked* `jti` is presented, treat it as a breach and revoke the user's entire token family. Logout becomes "revoke this `jti`". This is the single most valuable security upgrade to what you have.

**2. Audit log with a database trigger**
`core_audit_log (id, table_name, row_id, action, changed_by, changed_at, diff jsonb)`. Populate it with a trigger on `core_user` using `to_jsonb(OLD)`/`to_jsonb(NEW)`. Then add `SET LOCAL app.user_id = '...'` inside your transactions so the trigger knows who acted. This is how you get auditing without `@EntityListeners`.

**3. Filtering, sorting and pagination done properly**
Extend `ListUsers` with `sqlc.narg` filters for `status`, `role_id` and a name search, plus a whitelisted `sort` parameter mapped to a small set of SQL fragments. **Whitelist the sort column — never interpolate user input into SQL.** Add keyset pagination (`WHERE id > $lastID`) alongside offset pagination and explain to yourself why keyset is better at depth.

**4. A second resource, end to end**
Add `core_project` with a `project_owner_id` FK to `core_user`. Then the interesting part: authorization. A `USER` may create projects and read only their own; an `ADMIN` may read all; a `SUPER_USER` may delete any. Write the policy as a single method with a table-driven test covering every role × action combination.

**5. Optimistic locking**
Add a `version bigint not null default 0` column to `core_user`. Updates become `UPDATE ... SET version = version + 1 WHERE id = $1 AND version = $2` and return `:execrows`. Zero rows affected means someone else won the race → `409 Conflict`. This is manual `@Version`.

**6. Rate limiting**
Per-IP and per-account limits on `/auth/login`. Start with an in-memory token bucket (`golang.org/x/time/rate`) and be able to explain why that breaks with more than one replica — then move it to Redis. This is the Go equivalent of Bucket4j.

**7. Metrics and dashboards**
Expose `/metrics` with `prometheus/client_golang`: request count by route/status, request duration histogram, DB pool stats (`pool.Stat()`), and login failures. Add Prometheus + Grafana to Compose and build one dashboard. Watch a p95 latency chart respond to load.

**8. Distributed tracing**
Wire `go.opentelemetry.io/otel` so every request gets a trace, the `pgx` query becomes a span, and traces export to Jaeger. Then notice how much of what `slog` was doing for you gets subsumed by spans.

**9. CI**
GitHub Actions: `gofmt -l .`, `go vet ./...`, `staticcheck ./...`, `go test ./... -race -cover`, `go build ./...`, and a Docker build. Run the integration tests with a service container. Add a coverage gate once you know your baseline.

**10. Deploy it**
Fly.io, Railway, or a small VPS with systemd. The VPS version teaches the most: build a static binary, ship it with `scp`, a unit file with `EnvironmentFile=/etc/learn101/env`, `Restart=on-failure`, and a `SIGTERM`-based restart you can watch in `journalctl`. Then point your Android app at it.

**11. Concurrency**
Add a background worker that soft-deletes accounts inactive for 90 days, running on `time.Ticker`, cancelled by the same context that stops the HTTP server. Then add a `/api/v1/users/:uuid/stats` endpoint that fans out three independent queries with `errgroup.Group` and returns combined results.

**12. Fuzzing and benchmarks**
Add a `FuzzParseBearer(f *testing.F)` fuzz test to the auth middleware and let it run for a minute. Write `BenchmarkIssueToken` and `BenchmarkListUsers` and read the output with `-benchmem`. Use `go tool pprof` on a CPU profile — this is the whole performance toolkit, and it's in the standard distribution.

---

## 13.4 Signs you're writing Java in Go

Watch for these. They're all normal reflexes that produce code your future self will dislike.

| Symptom | What it means | Fix |
|---|---|---|
| A `Utils` / `Helper` package with 30 unrelated functions | you're avoiding naming the domain | put behaviour on a type, or name the package for its domain |
| Interfaces for everything, with one implementation | interfaces *first* is a Java habit | define interfaces when the *consumer* needs them (usually for tests) |
| `IUserService` / `UserServiceImpl` | naming from Java | name the interface after behaviour, the struct after implementation |
| Getters and setters on every field | encapsulation reflex | export the field; add a method only when it does work |
| Deep package hierarchy mirroring `com.company.app.user.service` | Java path-as-package | Go packages are flat; `internal/service` is the whole path |
| `context.Context` passed as a non-first param, or stored in a struct | Java habit of threading state | first param, always, never stored |
| `panic` for expected failures | using panic as `throw` | return an error |
| Wrapping everything in a struct with `New`/`Get`/`Set` and no logic | DTO-itis | pass the value |
| A `try`-shaped helper that just collects errors | translating exceptions | `if err != nil { return }` |
| Returning `interface{}` / `any` everywhere | avoiding types | use a concrete type or generics |
| A `Response` type with 20 nullable fields | avoiding distinct DTOs | one struct per endpoint shape |
| Fighting `gofmt` | formatting preference | stop; the formatter is the style guide |
| `sync.Mutex` around everything | `synchronized` reflex | prefer immutability; keep state in the DB |
| Long parameter lists instead of an options struct | Java's overload habit | `WithX` functional options, or a params struct |
| A background goroutine started in `init()` | annotate-and-forget | start it explicitly in `run()` so it can be cancelled |
| Comments explaining control flow | the control flow is too clever | restructure; Go code should be boring |

The last one is the real test. Idiomatic Go is **long and obvious**, not dense and clever. If a reviewer can't follow the flow top to bottom, the Go is wrong even if it works.

---

## 13.5 What to learn next

### Language depth
- **`context` mastery** — deadlines, cancellation trees, `context.WithValue` discipline. Read the package docs properly; it's short and it's load-bearing in every service you'll write.
- **Concurrency patterns** — worker pools, `errgroup`, fan-out/fan-in, pipelines, `select` with `done` channels. Then read about data races and the memory model.
- **Interfaces by design** — how to find the right small interface. Read the `io` package's definitions; they're a masterclass.
- **Generics in practice** — mostly for collections and builders. Resist using them where a concrete type is clearer.
- **`sync` beyond `Mutex`** — `WaitGroup`, `Once`, `atomic`, `singleflight`.

### Backend engineering
- **PostgreSQL performance** — `EXPLAIN ANALYZE`, index types (B-tree vs GIN vs partial vs covering), when a partial index wins (your soft-delete case), transaction isolation levels, and `SELECT ... FOR UPDATE` vs advisory locks.
- **Connection pooling at scale** — why `MaxConns × replicas` must stay under Postgres' `max_connections`, and when to add PgBouncer.
- **Background jobs** — a table-based queue with `SKIP LOCKED` is a fine first job queue; a real broker (NATS, RabbitMQ, Kafka) when you need it.
- **gRPC and protobuf** — the natural step when your Android app wants typed contracts, or when services talk to each other.
- **Observability** — OpenTelemetry traces + Prometheus metrics + structured logs, and the discipline of correlating all three via a request ID.
- **Caching** — what to cache, invalidation, and why the database is usually fast enough.

### Production
- **Deployment shapes** — containers on a managed platform, systemd on a VPS, Kubernetes when you actually need it.
- **Zero-downtime deploys** — the readiness probe and graceful shutdown you already built are the whole mechanism; now wire them to a rolling deploy.
- **Secrets management** — rotation, and what to do when `JWT_SECRET` leaks (issue tokens with a `kid`, keep two keys valid, rotate, retire).
- **Security review** — the OWASP API Top 10 against your own endpoints, deliberately. Chapter 07's gaps list is your starting point.

### Reading list

**Language**
- *A Tour of Go* — https://go.dev/tour/ — the starting point if any syntax still surprises you
- *Effective Go* — https://go.dev/doc/effective_go — the canonical idiom doc; read it end to end once
- *Go by Example* — https://gobyexample.com — task-shaped snippets for anything specific
- *Go Code Review Comments* — https://go.dev/wiki/CodeReviewComments — the de facto style guide beyond `gofmt`
- *Uber Go Style Guide* — https://github.com/uber-go/guide — opinionated, useful
- *Learning Go* (Jon Bodner) — the best modern book for an experienced developer
- *100 Go Mistakes and How to Avoid Them* (Teiva Harsanyi) — the highest-value book on this list for someone arriving from another language
- *Let's Go* and *Let's Go Further* (Alex Edwards) — building web services in Go, exactly this project's shape
- *The Go Programming Language* (Donovan & Kernighan) — the reference text

**Libraries in this stack**
- sqlc — https://docs.sqlc.dev
- pgx — https://github.com/jackc/pgx
- Gin — https://gin-gonic.com/docs/
- golang-migrate — https://github.com/golang-migrate/migrate
- `golang-jwt/jwt` — https://github.com/golang-jwt/jwt
- testcontainers-go — https://golang.testcontainers.org

**Adjacent**
- PostgreSQL docs — https://www.postgresql.org/docs/16/
- OWASP JWT cheat sheet — https://cheatsheetseries.owasp.org/cheatsheets/JSON_Web_Token_for_Java_Cheat_Sheet.html (the concepts apply regardless of language)
- *Designing Data-Intensive Applications* (Kleppmann) — not Go-specific, and the best backend book there is

One caution: `golang-standards/project-layout` is widely cited and is **not** an official standard. It's one person's opinion, and it over-structures small services. The layout in chapter 02 is deliberately smaller; grow into more structure only when a package becomes hard to navigate.

---

## 13.6 The honest summary

What Got **easier** coming from Spring Boot:

- No container, no bean wiring mystery, no `NoSuchBeanDefinitionException`.
- No ORM surprises. The SQL you write is the SQL that runs.
- One binary, ~20MB image, millisecond startup.
- No dependency-version conflicts ever again.
- Tests are function calls; `go test` finishes before you switch windows.
- You can read the entire standard library's source.

What got **harder**:

- You write more code for the same feature. Wiring, error checks, transaction handling, scanning — all explicit.
- No framework conveniences: no property binding, no validation framework defaults, no auto-configuration.
- Dependency injection is manual, so `app.Build` grows as the app does.
- Migrations, health endpoints, metrics — all hand-rolled or separately installed.
- Error handling is verbose by design, and you must resist collapsing it.
- Less "someone already solved this exact problem" than the Java ecosystem has.

The trade is **explicitness for convenience**. Everything the Spring container was doing for you is now visible in `../../main.go` and `app.Build`. That's the point — and for a service you have to operate at 2am, it's usually the right trade.

You've reached the end of the guide. Go build the next resource.

---

Back to the [index](README.md).
