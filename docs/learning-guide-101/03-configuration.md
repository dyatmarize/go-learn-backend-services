# Chapter 03 — Configuration and Secrets

> **Goal:** replace `application.yml` + `@ConfigurationProperties` with a typed config struct that fails fast on missing values. By the end, an unset `DATABASE_URL` stops the app at startup with a clear message instead of a mysterious nil-pointer panic three requests later.

---

## 3.1 The Spring Boot way, and why Go doesn't do it

In Spring Boot you write declarative configuration and the framework binds it:

```yaml
# application.yml
spring:
  datasource:
    url: ${DATABASE_URL}
    username: ${DB_USER}
    password: ${DB_PASSWORD}
app:
  jwt:
    secret: ${JWT_SECRET}
    access-ttl: 15m
```

```java
@ConfigurationProperties(prefix = "app.jwt")
@Validated
public record JwtProperties(@NotBlank String secret, Duration accessTtl) {}
```

There's nothing wrong with that. It's just not how Go works. Go has no property binding, no relaxed binding, no profiles, no EL (`${...}`), no `@Validated` on config. What it has:

1. **Environment variables** as the primary source — 12-factor style.
2. A **plain struct**.
3. A **function** that fills it and validates it.

That's the whole mechanism. It's ~60 lines and you can read all of it.

| Spring Boot | Go |
|---|---|
| `application.yml` / `application.properties` | `.env` for local dev, real env vars in deployment |
| `${ENV_VAR}` placeholder resolution | `os.Getenv("ENV_VAR")` |
| `@ConfigurationProperties` | a `Config` struct |
| `@Validated` + `@NotBlank` | a `validate()` method |
| `@Value("${...}")` | not needed — read the struct field |
| Profiles (`spring.profiles.active`) | one `APP_ENV` field and explicit `if`s |
| `@Profile("dev")` beans | separate wiring functions, selected in `app.Build` |
| `application-{profile}.yml` | `.env` vs real env; nothing fancier |
| Property reload at runtime | not supported — restart the process |
| Spring Cloud Config / Vault | call the secret store's API at startup, or inject env vars |

**Design rule:** configuration is read **once, at startup**, validated, and then passed down as a struct. No package should call `os.Getenv` at request time. That's the Go equivalent of Spring's "no `@Value` in business logic" advice, and here it's enforced by making `config.Config` the only holder of the values.

---

## 3.2 `.env` for local development

`.env` is a developer convenience. It is **not** a production mechanism — in production the platform (Docker, Kubernetes, systemd, ECS) injects real environment variables.

The repo already has an empty `.env`. Fill it:

```dotenv
# .env — local development only. NEVER commit this file.
APP_ENV=development

HTTP_ADDR=:8080

DATABASE_URL=postgres://learn101:learn101@localhost:5432/learn101?sslmode=disable

# Generate with: openssl rand -base64 48
JWT_SECRET=replace-me-with-a-long-random-string

JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=168h

LOG_LEVEL=debug
```

And commit a template with no secrets:

```dotenv
# .env.example — committed. Copy to .env and fill in.
APP_ENV=development
HTTP_ADDR=:8080

DATABASE_URL=postgres://learn101:learn101@localhost:5432/learn101?sslmode=disable

# Generate with: openssl rand -base64 48
JWT_SECRET=

JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=168h
LOG_LEVEL=debug
```

```bash
$ cp .env.example .env
$ openssl rand -base64 48   # paste into JWT_SECRET
```

### Loading it: `godotenv`

`github.com/joho/godotenv` is already imported in `../../main.go`. Two behaviours matter:

1. **`Load` does not override existing environment variables.** If `DATABASE_URL` is already set in your shell, `.env` cannot clobber it. That's the right precedence: real environment wins.
2. **A missing `.env` is not fatal.** In production there is no `.env`, and that must not crash the app.

Which brings us to the existing bug in `../../main.go`:

```go
func main() {
	err := godotenv.Load(".env")
}
```

That error is silently discarded. In development, a typo in `.env` means every variable is silently unset. Distinguish "file not found" from "file is broken":

```go
if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
	// A malformed .env should be loud. A missing .env is fine.
	logger.Warn("could not load .env", "err", err)
}
```

`godotenv.Load()` with no arguments loads `.env` from the **current working directory**, which is why your GoLand run configuration's working directory matters (chapter 02).

---

## 3.3 The config package

🧩 `internal/config/config.go` — a complete file:

```go
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config is the single source of truth for process configuration.
// It is populated once at startup and then treated as read-only.
type Config struct {
	AppEnv      string
	HTTPAddr    string
	DatabaseURL string
	JWTSecret   string
	AccessTTL   time.Duration
	RefreshTTL  time.Duration
	LogLevel    slog.Level
}

// Load reads configuration from the environment, applies defaults,
// and validates required values. It returns an error listing every
// problem it found rather than only the first.
func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:      getString("APP_ENV", "development"),
		HTTPAddr:    getString("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
	}

	var errs []error

	accessTTL, err := getDuration("JWT_ACCESS_TTL", 15*time.Minute)
	if err != nil {
		errs = append(errs, err)
	}
	cfg.AccessTTL = accessTTL

	refreshTTL, err := getDuration("JWT_REFRESH_TTL", 168*time.Hour) // 7 days
	if err != nil {
		errs = append(errs, err)
	}
	cfg.RefreshTTL = refreshTTL

	level, err := getLogLevel("LOG_LEVEL", slog.LevelInfo)
	if err != nil {
		errs = append(errs, err)
	}
	cfg.LogLevel = level

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if cfg.JWTSecret == "" {
		errs = append(errs, errors.New("JWT_SECRET is required"))
	} else if len(cfg.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must be at least 32 characters"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// IsProduction reports whether the app is running in a production environment.
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production")
}

// IsDevelopment reports whether the app is running locally.
func (c *Config) IsDevelopment() bool {
	return strings.EqualFold(c.AppEnv, "development")
}

func getString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw) // "15m", "168h", "30s"
	if err != nil {
		return fallback, fmt.Errorf("%s: %q is not a valid duration: %w", key, raw, err)
	}
	return d, nil
}

func getLogLevel(key string, fallback slog.Level) (slog.Level, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(raw))); err != nil {
		return fallback, fmt.Errorf("%s: %q is not a valid log level: %w", key, raw, err)
	}
	return level, nil
}
```

Things worth noticing, because they're Go idioms rather than Java ones:

- **`errors.Join`** collects every validation failure and returns them as one error with newline-separated messages. Compare to Spring, which reports all binding errors at once via `BindException` — same spirit, six lines of code.
- **`time.ParseDuration`** understands `300ms`, `15m`, `168h`. No `Duration` parsing library, no `@DurationUnit`.
- **`slog.Level.UnmarshalText`** parses `"debug"`, `"INFO"`, `"warn"`, `"error"`. The stdlib does level parsing for you.
- **Defaults live in `Load`**, next to the lookup. There's no `.env.example`-driven default, no relaxed binding, no `application-default.yml`.
- **No struct tags.** No reflection. You can read the whole thing.

### What "fail fast" looks like

```bash
$ DATABASE_URL= JWT_SECRET="$(openssl rand -base64 48)" ./bin/api
{"time":"...","level":"ERROR","msg":"fatal","err":"invalid configuration: DATABASE_URL is required"}
$ echo $?
1
```

Three details in that output are worth noticing:

1. `msg` is `"fatal"` — that's `main`'s wrapper. The real cause is in `err`.
2. With only one problem there is **no newline** in the message. `errors.Join` separates multiple failures with newlines, so unset both variables and you'll see `invalid configuration: \nDATABASE_URL is required\nJWT_SECRET is required`.
3. The line is plain text, not JSON — the JSON handler isn't installed until after `config.Load` succeeds. That's fine for a fatal startup path, but it means you must read early boot logs as text.

That's the goal: the process refuses to start, tells you exactly what's wrong, and exits non-zero so your orchestrator restarts or alerts instead of serving broken traffic.

---

## 3.4 Wiring it into `main`

🧩 `cmd/api/main.go` at the end of this chapter (it grows in later chapters):

```go
package main

import (
	"errors"
	"log/slog"
	"os"

	"github.com/joho/godotenv"

	"learn101/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run keeps main() free of os.Exit so that deferred cleanup always executes.
func run() error {
	// Load .env for local development. Real environment variables take
	// precedence, and a missing file is expected in production.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not load .env", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting",
		"env", cfg.AppEnv,
		"addr", cfg.HTTPAddr,
	)
	return nil
}
```

The `main()` / `run()` split is a widely used Go pattern: `os.Exit` skips deferred functions, so any real logic belongs in `run()` where `defer` works. You'll see this pay off in chapter 11 when `run()` owns the database pool and the HTTP server.

Why `slog.SetDefault(logger)`? So packages that don't receive a `*slog.Logger` still log through the configured handler via `slog.Info(...)`. Passing a logger explicitly is better in libraries; for an application binary, setting the default is pragmatic.

---

## 3.5 The DSN

`DATABASE_URL` is a libpq-style connection string. pgx accepts both URL and keyword/value forms:

```dotenv
# URL form (preferred — works everywhere, easy to set in Docker/K8s)
DATABASE_URL=postgres://learn101:learn101@localhost:5432/learn101?sslmode=disable

# keyword/value form (equivalent)
DATABASE_URL=host=localhost port=5432 user=learn101 password=learn101 dbname=learn101 sslmode=disable
```

Notes:

- `sslmode=disable` is correct for local Docker Postgres. In production, use `sslmode=require` or `verify-full` — never ship `disable`.
- Don't hand-concatenate username/password into a DSN string. Password characters like `@`, `:`, `/` will break it. Use `net/url` if you must build it programmatically.
- Pool tuning (max connections, idle time) is **not** done in the DSN here; it's done with `pgxpool.ParseConfig` in chapter 04, which is more readable and lets you set values not expressible in a URL.

---

## 3.6 Secrets discipline

Three rules, in priority order:

1. **Never commit a real secret.** `.env` is in `.gitignore` (chapter 02). `.env.example` has placeholders only.
2. **Generate secrets, don't invent them.** `openssl rand -base64 48`. A human-chosen JWT secret is a crackable JWT secret.
3. **In production, inject via the platform.** Docker Compose `environment:` / `env_file:`, Kubernetes Secrets, AWS Secrets Manager. If your platform supports fetching secrets at startup, do that in `config.Load` — the rest of the app doesn't care where the value came from.

The one thing to be careful about: **`.env` is not a secret store.** It's a text file on a laptop. Treat it as convenient defaults, not protection.

Also worth doing now, given `.env` is currently empty: confirm it really is ignored.

```bash
$ git check-ignore -v .env
.gitignore:12:.env	.env
```

If that prints nothing, `.env` is not ignored and you should fix `.gitignore` before adding any real value to it.

---

## 3.7 Testing the config loader

Because `Load` reads from the process environment, tests set variables with `t.Setenv`, which automatically restores them afterwards:

```go
package config_test

import (
	"testing"
	"time"

	"learn101/internal/config"
)

func TestLoad(t *testing.T) {
	t.Run("requires DATABASE_URL", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("applies defaults", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
		t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")
		t.Setenv("JWT_ACCESS_TTL", "")
		t.Setenv("APP_ENV", "")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AppEnv != "development" {
			t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, "development")
		}
		if cfg.AccessTTL != 15*time.Minute {
			t.Errorf("AccessTTL = %v, want %v", cfg.AccessTTL, 15*time.Minute)
		}
	})
}
```

`t.Setenv` is Go's answer to `@TestPropertySource` plus `@DirtiesContext` — scoped, automatic, no context cache to invalidate. Note the table-free style here; chapter 09 covers table-driven tests, which are better when you have many cases of the same shape.

---

## 3.8 Exercises

1. Generate a real `JWT_SECRET` with `openssl rand -base64 48` and add it to `.env`.
2. Start the app with `.env` present, then with `.env` renamed away and `DATABASE_URL` unset. Confirm the second case exits 1 with a clear message.
3. Set `JWT_ACCESS_TTL=15 minutes` (invalid). Confirm the error names the variable and echoes the bad value.
4. Add a `SMTP_URL` config field that is required only when `APP_ENV=production`. Hint: validate conditionally inside `Load` after the unconditional checks.
5. Break the DSN deliberately — put an `@` in the password. Watch the connection fail, then fix it by URL-encoding the password with `url.QueryEscape` (or just use a password without special characters locally).
6. Write a third `TestLoad` case asserting that a short `JWT_SECRET` (under 32 chars) is rejected.

## 3.9 Done when

- [ ] `.env` is populated, and `git check-ignore -v .env` confirms it is ignored.
- [ ] `.env.example` is committed and contains no real secrets.
- [ ] `config.Load()` fails loudly on a missing `DATABASE_URL` or a short `JWT_SECRET`.
- [ ] `../../main.go` no longer discards the `godotenv.Load` error.
- [ ] `go test ./internal/config/...` passes.
- [ ] Nothing outside `internal/config` calls `os.Getenv`.

Next: [Chapter 04 — PostgreSQL, Docker and Migrations](04-postgres-docker-and-migrations.md).
