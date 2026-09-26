# Chapter 11 — Docker and Graceful Shutdown

> **Goal:** ship the API as a small container image and shut down without dropping in-flight requests. By the end, `docker compose up` runs your API and Postgres together, and `Ctrl-C` drains cleanly.
>
> **Never used Docker?** Read [Chapter 10 — Docker from Zero](10-docker-from-zero.md) first. This chapter assumes that vocabulary — images, layers, volumes, registries, `docker run` flags and Compose.

Spring Boot gives you an embedded Tomcat inside a fat JAR and a shutdown hook you configure with `server.shutdown=graceful`. Go gives you a binary and about 40 lines of shutdown logic. The concepts map cleanly.

---

## 11.1 Why Go containers are different

| Spring Boot | Go |
|---|---|
| Fat JAR (~50MB) + a JRE (~200MB) | a single static binary (~15MB), no runtime |
| `openjdk:21-jre` base image | `scratch` or `distroless` (no OS at all) |
| JVM warmup, JIT, heap sizing | starts in milliseconds, no warmup |
| `-Xmx`, GC tuning flags | `GOMEMLIMIT` if you ever need it |
| Layered JARs for caching | `go mod download` in a cache layer |
| `server.shutdown=graceful` | `srv.Shutdown(ctx)` |
| Embedded Tomcat lifecycle | `http.Server` + `signal.NotifyContext` |
| `@PreDestroy` | `defer` plus explicit cleanup order |

The practical upshot: a Go image is roughly 20x smaller, starts instantly, and has no runtime to patch. Cross-compilation (chapter 02) means you can even build the binary outside Docker and just `COPY` it in.

---

## 11.2 A multi-stage Dockerfile

Two stages: a builder with the Go toolchain, and a runtime with nothing but your binary and TLS certificates.

🧩 `Dockerfile`:

```dockerfile
# ---------- build stage ----------
FROM golang:1.27-alpine AS build

WORKDIR /src

# Copy dependency manifests first so this layer is cached
# independently of your source. Same idea as Maven's dependency:go-offline.
COPY go.mod go.sum ./
RUN go mod download

# Now copy the source and build.
COPY . .

# CGO_ENABLED=0 produces a static binary: no libc, so a scratch-like
# runtime image works. -trimpath strips local paths; -s -w strip the
# symbol table and DWARF data, cutting the binary substantially.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api ./cmd/api

# ---------- runtime stage ----------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api /api

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/api"]
```

Notes that matter:

- **`distroless/static`** has no shell, no package manager, no `curl`. That's the point: nothing to exploit. It also means you cannot `docker exec` into it — see §11.5 for the debugging-friendly variant.
- **`nonroot`** runs as UID 65532. Never run a service as root.
- **`COPY go.mod go.sum ./` before `COPY . .`** — layer caching. Dependencies change far less often than code, so `go mod download` is usually cached. Dropping the `.` copy first would invalidate it on every edit.
- **`CGO_ENABLED=0`** is what makes the static binary possible. If you ever add a dependency needing C (some SQLite drivers), you lose distroless/static and must use a base with glibc or musl.
- **`-ldflags="-s -w"`** is optional but free.
- **Build context matters.** `.` as the final `COPY` source means the whole repo goes into the builder — including `docs/` and `.git`. Trim it with `.dockerignore`.

🧩 `.dockerignore`:

```
.git
.gitignore
.idea
.commandcode
bin
docs
coverage.out
*.md
.env
docker-compose.yml
Dockerfile
```

Note that `.env` is excluded. Secrets are injected at runtime, never baked into an image — an image layer is readable by anyone who can pull it.

---

## 11.3 Graceful shutdown in `../../main.go`

This is where the whole chapter lands. The final shape of `../../main.go`:

🧩 `cmd/api/main.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"learn101/internal/app"
	"learn101/internal/config"
	"learn101/internal/dbpool"
	"learn101/internal/logging"
)

func main() {
	// os.Exit skips deferred functions, so all real work happens in run(),
	// where defer and cleanup are reliable.
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Configuration.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not load .env", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg)
	slog.SetDefault(log)

	// 2. A context cancelled by SIGINT or SIGTERM.
	//    This is what makes Ctrl-C and `docker stop` behave.
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 3. Dependencies. Cancelled context => startup is abortable.
	pool, err := dbpool.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close() // runs last (LIFO): after the server has stopped

	// 4. The object graph and the HTTP handler.
	router := app.Build(cfg, pool, log)

	// 5. Server timeouts. Never run an http.Server without them.
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,  // Slowloris protection
		ReadTimeout:       15 * time.Second, // full request incl. body
		WriteTimeout:      30 * time.Second, // response write budget
		IdleTimeout:       60 * time.Second, // keep-alive reuse window
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	// 6. Listen in a goroutine so main can also watch for signals.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening",
			"addr", srv.Addr,
			"env", cfg.AppEnv,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("listen and serve: %w", err)
		}
	}()

	// 7. Block until either the server dies or a signal arrives.
	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	// 8. Stop accepting new connections and wait for in-flight
	//    requests. Use a FRESH context: ctx is already cancelled.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Timed out waiting for handlers; close hard.
		if closeErr := srv.Close(); closeErr != nil {
			log.Error("forced close failed", "err", closeErr)
		}
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	log.Info("shutdown complete")
	return nil
}
```

### Why each piece exists

**`signal.NotifyContext`** — a `context.Context` that gets cancelled when the process receives `SIGINT` (Ctrl-C) or `SIGTERM` (`docker stop`, Kubernetes). One line replaces a manual `signal.Notify` channel loop.

**The `main`/`run` split** — `os.Exit` does not run deferred functions. Putting everything in `run()` means `defer pool.Close()` actually executes. This is a real bug in a lot of Go services.

**`select` on two channels** — you need to react to *both* "the server failed to start" and "we were signalled". Blocking directly on `ListenAndServe` can't do that.

**A fresh context for shutdown** — this trips people up. `ctx` is already cancelled at that point (that's *why* you're shutting down), so passing it to `Shutdown` returns immediately and drops every in-flight request. `Shutdown` needs its own deadline.

**`srv.Shutdown` vs `srv.Close`** — `Shutdown` stops accepting new connections and waits for active requests; `Close` kills everything immediately. Use `Shutdown`, and `Close` only as the timeout fallback.

**`ReadHeaderTimeout`** deserves special mention. Without it, a client that opens a connection and sends one byte per minute holds a connection forever. It's a one-line denial-of-service fix, and it's the single most commonly omitted setting in Go HTTP servers.

**`BaseContext`** wires the signal context into every request's `context.Context`. Now when the process is signalled, `c.Request.Context()` is cancelled too — which propagates into your `pgx` queries and lets them abort instead of running to completion during shutdown.

### The shutdown sequence, in order

```
SIGTERM
  │
  ├─ ctx cancelled ──► in-flight request contexts cancelled
  │                    └─► pgx aborts long queries
  │
  ├─ srv.Shutdown()
  │    ├─ stop accepting new connections (listener closed)
  │    ├─ wait for active handlers to return  (up to 15s)
  │    └─ close idle keep-alive connections
  │
  ├─ defer pool.Close()   ── no connections left to hold
  └─ process exits 0
```

Deferred functions run LIFO, so `pool.Close()` (registered before the server logic) runs **after** `Shutdown` returns. That's the correct order: the pool must outlive the requests that use it. If you closed the pool first, in-flight handlers would fail with "pool closed".

### Comparison to Spring Boot

| Spring Boot | Go |
|---|---|
| `server.shutdown=graceful` | `srv.Shutdown(ctx)` |
| `spring.lifecycle.timeout-per-shutdown-phase=15s` | `context.WithTimeout(..., 15s)` |
| Shutdown hook order (`@PreDestroy`, `DisposableBean`) | `defer` order — LIFO, and visible in one function |
| `ThreadPoolTaskExecutor` draining | `Shutdown` waiting on handler goroutines |
| `server.tomcat.max-connections` | `http.Server` has no accept queue limit; use your ingress |
| `server.tomcat.connection-timeout` | `ReadHeaderTimeout` |
| Actuator graceful shutdown endpoint | signal handling, above |

The reconciliation is stark: Spring spreads lifecycle across configuration properties and annotations; Go puts it in 40 lines of one function, in execution order, top to bottom.

---

## 11.4 Compose: API + database together

Extend the Compose file from chapter 04.

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

  # Migrations run as a one-shot job, NOT on app boot.
  # This is the fix for Spring's migrate-on-startup race.
  migrate:
    image: migrate/migrate:v4.17.1
    container_name: learn101-migrate
    volumes:
      - ./db/migrations:/migrations:ro
    command:
      - "-path=/migrations"
      - "-database=postgres://learn101:learn101@postgres:5432/learn101?sslmode=disable"
      - "up"
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"

  api:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: learn101-api
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
    environment:
      APP_ENV: production
      HTTP_ADDR: ":8080"
      # NOTE: hostname is the service name ("postgres"), not localhost.
      DATABASE_URL: postgres://learn101:learn101@postgres:5432/learn101?sslmode=disable
      # Required: Compose fails fast if this is unset. Never hardcode a default secret.
      JWT_SECRET: ${JWT_SECRET:?JWT_SECRET must be set in the environment or .env}
      JWT_ACCESS_TTL: 15m
      JWT_REFRESH_TTL: 168h
      LOG_LEVEL: info
    ports:
      - "8080:8080"
    # SIGTERM is passed through; the app drains for up to 15s.
    stop_grace_period: 20s

volumes:
  postgres-data:
```

Details worth understanding:

- **`depends_on` with `condition`** — plain `depends_on` only waits for the container to *start*, not to be *ready*. `service_healthy` waits for the healthcheck; `service_completed_successfully` waits for the migration job to exit 0. Without these you get a race where the API boots before the schema exists.
- **The `migrate` service is the payoff** of chapter 04's advice. It runs once, it's idempotent, and the API never migrates on boot. With multiple API replicas, this is the difference between one clean migration and N racing ones.
- **`DATABASE_URL` uses `postgres` as the host** — the Compose service name resolves via the internal DNS. `localhost` inside the API container would be the API container itself. This is the single most common Compose mistake.
- **`${JWT_SECRET:?...}`** makes Compose refuse to start if the variable is unset. Fail fast, in the same spirit as `config.Load`.
- **`stop_grace_period: 20s`** must exceed your 15s `Shutdown` timeout, otherwise Docker sends `SIGKILL` and you drop requests anyway. Getting this backwards is subtle and common.
- **No `env_file: .env`** here — in production the environment comes from the platform. For local runs you can add `env_file: [.env]` to the `api` service.

```bash
$ export JWT_SECRET="$(openssl rand -base64 48)"
$ docker compose up -d --build
$ docker compose ps
$ docker compose logs -f api
$ curl -s localhost:8080/readyz
```

Test the shutdown path explicitly:

```bash
$ docker compose stop api        # sends SIGTERM, waits for graceful exit
$ docker compose logs api | tail -5
# expect: "shutdown signal received, draining connections" then "shutdown complete"
```

---

## 11.5 A debugging-friendly variant

Distroless has no shell, so you can't `kubectl exec -it ... -- sh`. If you need that during development, swap the runtime stage:

```dockerfile
# ---------- runtime stage (debug variant) ----------
FROM alpine:3.20

# ca-certificates for outbound TLS; tzdata for correct local time;
# curl for healthchecks and manual poking.
RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=build /out/api /api

# Create an unprivileged user (alpine has no "nonroot" by default).
RUN adduser -D -u 10001 app
USER app

EXPOSE 8080
ENTRYPOINT ["/api"]
```

Now you can add a real container healthcheck to the `api` service (distroless can't run one, which is why §11.4 omits it):

```yaml
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://localhost:8080/readyz"]
      interval: 10s
      timeout: 3s
      retries: 3
      start_period: 5s
```

Use distroless in production, alpine locally. Or just use alpine for both — a few extra MB for a lot less friction is a defensible trade for a project this size.

---

## 11.6 Image size and build speed

```bash
$ docker build -t learn101-api .
$ docker images learn101-api
REPOSITORY    TAG      SIZE
learn101-api  latest   ~18MB        # distroless/static + binary

# Compare: a Spring Boot fat JAR + JRE is typically 250-350MB.
```

Speed tricks, in order of payoff:

1. **`.dockerignore`** — the build context is uploaded to the daemon on every build. Excluding `docs/`, `.git/`, `bin/` makes builds noticeably faster and avoids invalidating the `COPY . .` layer for documentation changes.
2. **Layer order** — `go.mod`/`go.sum` before source. Already done.
3. **`--cache-from` / BuildKit cache mounts** if CI builds get slow:
   ```dockerfile
   RUN --mount=type=cache,target=/go/pkg/mod \
       --mount=type=cache,target=/root/.cache/go-build \
       go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
   ```
4. **`docker build --target build`** to stop at the builder stage when you only want to compile.

---

## 11.7 Exercises

1. Build the image and check the size. Then remove `-ldflags="-s -w"` and rebuild — compare.
2. Run it: `docker run --rm -p 8080:8080 --env-file .env -e DATABASE_URL=... learn101-api`. Note that `localhost` in `DATABASE_URL` won't reach a Postgres on your host; use `host.docker.internal` (or `--network host` on Linux).
3. `docker compose up -d --build` and confirm the order: `postgres` healthy → `migrate` exits 0 → `api` starts.
4. **Verify graceful drain.** Add a temporary endpoint that sleeps 5 seconds, start a request against it, and `docker compose stop api` mid-request. Confirm the request completes with a 200 and the logs show the drain messages. Then remove the endpoint.
5. Break the grace periods: set `stop_grace_period: 2s` and repeat exercise 4. Confirm the request is killed. Restore it to 20s. This is the bug you avoid by reading §11.4.
6. Remove `BaseContext` from the server and re-run exercise 4 with a slow database query. Observe the difference in whether the query is cancelled.
7. Deliberately omit `ReadHeaderTimeout` and reason about (or demonstrate with `nc`) how slowly sending headers holds a connection.
8. Switch to the alpine runtime and add the `curl` healthcheck. Confirm `docker compose ps` reports `api` as healthy.

## 11.8 Done when

- [ ] `docker compose up -d --build` brings up Postgres, migrates, and starts the API with one command.
- [ ] `docker compose stop api` logs a graceful drain and exits 0, with no dropped in-flight requests.
- [ ] `stop_grace_period` exceeds your shutdown timeout, and you know why.
- [ ] The image is under ~25MB and contains no shell (distroless) — or you consciously chose alpine and can say why.
- [ ] `.dockerignore` excludes `.env`, so no secret is baked into a layer.
- [ ] The API container's `DATABASE_URL` uses the Compose service hostname, not `localhost`.

Next: [Chapter 12 — Spring Boot → Go Cheat Sheet](12-spring-boot-to-go-cheatsheet.md).
