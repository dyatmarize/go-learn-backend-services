# Chapter 02 — Modules, Layout and Tooling

> **Goal:** replace the Maven mental model with Go modules, lay out the project so it doesn't become a junk drawer, and get a lint/format/build loop that runs faster than you're used to.

---

## 2.1 The build system is `go`

There is no `pom.xml` + Maven + plugins. There is one tool, `go`, and one config file per module.

```bash
$ go version
go version go1.27 linux/amd64

$ go mod init learn101          # already done in this repo
$ go mod tidy                   # add missing requires, drop unused ones
$ go build ./...                # compile everything
$ go run ./cmd/api              # compile + run
$ go test ./...                 # run all tests
$ go vet ./...                  # static checks
$ gofmt -l -w .                 # format in place
```

### `go.mod` and `go.sum` vs `pom.xml`

Current `go.mod` in this repo:

```
module learn101

go 1.27
```

That's it. After `go mod tidy` (which you should run now — the repo imports `godotenv` but has no `require` block and no `go.sum`), it grows to something like:

```
module learn101

go 1.27

require (
	github.com/gin-gonic/gin v1.11.0
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/jackc/pgx/v5 v5.8.0
	github.com/joho/godotenv v1.5.1
)

require ( /* indirect dependencies */ )
```

| Maven | Go |
|---|---|
| `pom.xml` | `go.mod` |
| `pom.xml` resolved lockfile (none by default) | `go.sum` — **checksums, always committed** |
| `<dependency>` | `require` |
| `<scope>test</scope>` | tests live in the same module; no separate scope |
| Maven Central | The module proxy (default `proxy.golang.org`), configured via `GOPROXY` |
| `settings.xml` mirrors/repos | `GOPROXY`, `GONOSUMDB`, `GOFLAGS` env vars |
| Version ranges / dependency mediation | **Minimal Version Selection** — one version per module, no ranges, no conflicts |
| `mvn dependency:tree` | `go mod graph` / `go mod why -m <module>` |
| Multi-module reactor | Multiple `go.mod` files + `go.work` (only if you truly need it) |

Two things to appreciate:

- **You will never resolve a dependency conflict again.** MVS picks the highest version any module requires, period. One version per module in the build.
- **Versions are pinned in `go.mod`; integrity is guaranteed by `go.sum`.** `go.sum` holds a content hash per module version, and `go build` verifies those hashes and fails on a mismatch. That's why `go.sum` must be committed.

Useful commands:

```bash
$ go get github.com/gin-gonic/gin@latest      # add / bump a dependency
$ go get github.com/gin-gonic/gin@v1.11.0     # pin an exact version
$ go get -u ./...                             # upgrade everything (careful)
$ go mod why github.com/joho/godotenv          # who needs this and why
$ go list -m all                              # every module in the build
$ go mod verify                               # re-check checksums
$ go clean -modcache                          # nuke the cache when things get weird
```

Vendoring (committing a `vendor/` directory, closest to Maven's offline behaviour) is optional:

```bash
$ go mod vendor     # writes vendor/, then builds use it automatically
```

Skip it for this project — `go.sum` plus the module cache is enough.

### Cross-compilation (you'll like this)

Java's "build once, run anywhere" needs a JVM. Go compiles a **static binary for any target, from any host**:

```bash
$ GOOS=linux   GOARCH=amd64 go build -o bin/api-linux    ./cmd/api
$ GOOS=windows GOARCH=amd64 go build -o bin/api.exe      ./cmd/api
$ GOOS=darwin  GOARCH=arm64 go build -o bin/api-mac      ./cmd/api
```

No JVM, no runtime dependency. A ~15MB binary that runs on a bare `scratch` container. Chapter 11 exploits this.

---

## 2.2 Project layout

Standard Go layout is a set of conventions, not a specification. Here's the layout this guide uses, and why:

```
learn101/
├── cmd/
│   └── api/
│       └── main.go              # entrypoint: load config, wire, serve
├── internal/
│   ├── app/
│   │   └── app.go               # the object graph: Build / wiring
│   ├── config/
│   │   └── config.go
│   ├── db/                      # sqlc OUTPUT — generated, don't hand-edit
│   │   ├── db.go
│   │   ├── models.go
│   │   ├── querier.go
│   │   └── user.sql.go
│   ├── domain/
│   │   ├── user.go
│   │   └── errors.go
│   ├── repository/
│   │   └── user_repository.go   # wraps internal/db, implements domain interfaces
│   ├── service/
│   │   └── user_service.go      # business rules
│   ├── handler/
│   │   ├── user_handler.go      # HTTP: bind, validate, call service, write response
│   │   ├── auth_handler.go
│   │   └── health_handler.go
│   ├── middleware/
│   │   ├── auth.go
│   │   ├── requestid.go
│   │   └── logger.go
│   └── auth/
│       └── jwt.go               # token issue/verify + password hashing
├── db/
│   ├── migrations/
│   │   ├── 000001_initial_schema.up.sql
│   │   └── 000001_initial_schema.down.sql
│   └── queries/
│       ├── user.sql
│       └── role.sql
├── docs/                        # this guide — invisible to the toolchain
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── sqlc.yaml
├── .env                         # gitignored
├── .env.example                 # committed
├── .gitignore
├── go.mod
└── go.sum
```

### `cmd/`

Each subdirectory is one binary. `cmd/api` is your HTTP server. Later, `cmd/migrate` or `cmd/worker` would sit alongside it, each with a thin `../../main.go` that does nothing but wire and start. This is why `../../main.go` should stay small — it's a composition root, not a place for logic.

### `internal/` — enforced privacy

This is a genuine Go feature, not a convention. **The compiler refuses to let code outside this module import anything under `internal/`.**

```
learn101/internal/service  → importable only by learn101/...
learn101/pkg/whatever      → importable by anyone
```

So `internal/` is your "not a public API" boundary, enforced by the toolchain. Since nothing else will ever import `learn101`, everything goes in `internal/`.

### Layering: how the packages depend on each other

```
        cmd/api
           │
           ▼
      internal/app ──────────┐
           │                  │
   ┌───────┴───────┐          │
   ▼               ▼          ▼
handler ──────► service ──► repository ──► db (sqlc)
   │               │            │
   └───────────────┴────────────┘
                   ▼
                domain
```

Rules that keep this from turning into spaghetti:

- **`domain` depends on nothing.** It holds your structs, your sentinel errors, and the interfaces your services need. No Gin, no pgx, no sqlc.
- **`handler` knows Gin.** Nothing below it does. Swapping Gin for `chi` should touch only this package and `app`.
- **`repository` knows sqlc and pgx.** Nothing above it does. This is the only place `pgx.ErrNoRows` is translated into `domain.ErrNotFound`.
- **`service` knows only `domain`.** It takes interfaces, returns domain types, and holds the business rules.

This layering is what lets chapter 09 test a service with a hand-written fake — no database, no HTTP server.

### Layout mapping from Maven

| Maven / Spring | Go here | Note |
|---|---|---|
| `src/main/java/com/example/...` | `internal/...` | No package-name-as-directory-path nesting. `internal/config`, not `internal/com/example/config`. |
| `src/main/resources/` | files next to the code, or embedded with `embed` | Use `//go:embed` to bake files into the binary. |
| `src/test/java/...` | `*_test.go` **next to the code being tested** | No parallel source tree. `user_service.go` and `user_service_test.go` sit side by side. |
| `src/main/java/.../Application.java` | `cmd/api/main.go` | |
| `@Configuration` classes | `internal/app/` | Wiring, explicit. |
| `application.yml` | `.env` + env vars | Chapter 03. |
| `db/migration/` (Flyway) | `db/migrations/` | Same convention, different tool. |

---

## 2.3 Fixing the repo's `.gitignore`

Right now `.gitignore` contains exactly one line:

```
/.commandcode
```

That means the compiled `learn101` binary at the repo root is **tracked in git**, which is why `git ls-files` shows it. Committing build output bloats the repo and causes noisy diffs. Replace `.gitignore` with this:

```gitignore
# Build output
/bin/
/api
/learn101
*.exe

# Test/coverage artifacts
*.out
coverage.html

# Env files — never commit real secrets
.env
.env.local

# Editor / tooling
/.commandcode
/.idea/workspace.xml
/.idea/shelf/

# OS noise
.DS_Store
```

Then untrack the binary that's already committed:

```bash
$ git rm --cached learn101
$ git rm --cached .env    # only if it's actually tracked
```

`git rm --cached` removes it from the index while leaving the file on disk. Worth noting: `.env` is empty today, which is a good moment to establish the habit — see the next chapter for `.env.example`.

Also run this once to generate the missing lockfile:

```bash
$ go mod tidy
$ ls go.sum
```

---

## 2.4 Formatting and linting

### `gofmt` is not optional

Go has one blessed format, produced by `gofmt`. There is no style debate, no `.editorconfig`, no Checkstyle config.

```bash
$ gofmt -l -w .     # -l lists files that differ, -w writes them
$ gofmt -l .        # list only (use in CI)
```

Better, `goimports` also adds and removes imports:

```bash
$ go install golang.org/x/tools/cmd/goimports@latest
$ goimports -l -w .
```

GoLand runs this on save automatically — enable **Settings → Tools → Actions on Save → Reformat code + Optimize imports**.

`gofmt` also enforces brace placement. Go requires the opening brace on the **same line**:

```go
// compiles
if x > 0 {
}

// does NOT compile — automatic semicolon insertion
if x > 0
{
}
```

### `go vet`

Catches real bugs the compiler allows — printf mismatches, unreachable code, suspicious struct copies:

```bash
$ go vet ./...
```

### Static analysis

| Java | Go |
|---|---|
| SpotBugs / PMD / Checkstyle | `staticcheck`, `golangci-lint` |
| SonarQube | `golangci-lint` with a config file |

```bash
# staticcheck: fast, opinionated, catches real problems
$ go install honnef.co/go/tools/cmd/staticcheck@latest
$ staticcheck ./...

# golangci-lint: the aggregator (many linters behind one command)
$ go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
$ golangci-lint run
```

### `go vet` + `gofmt` in one shot

The minimum CI check for any Go repo:

```bash
$ test -z "$(gofmt -l .)" && go vet ./... && go test ./... -race
```

Note the `test -z "$(...)"` wrapper. `gofmt -l` **prints** the offending files and still **exits 0**, so chaining it with `&&` would never fail the build. This wrapper is the standard CI idiom for turning its output into an exit status.

---

## 2.5 Hot reload

Java devs are used to DevTools / JRebel. The Go equivalent is `air`:

```bash
$ go install github.com/air-verse/air@latest
$ air
```

Or, since Go compilation is fast enough that you often don't need it:

```bash
$ go run ./cmd/api
```

A one-line watcher using `entr`, if you prefer:

```bash
$ find . -name '*.go' | entr -r go run ./cmd/api
```

---

## 2.6 Makefile — the closest thing to the Maven lifecycle

A `Makefile` at the repo root documents every command the project needs. This is idiomatic Go, not a workaround.

```makefile
.PHONY: help run build test lint fmt vet tidy migrate-up migrate-down sqlc docker-up docker-down

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

run: ## Run the API locally
	go run ./cmd/api

build: ## Build the binary
	go build -o bin/api ./cmd/api

test: ## Run tests with the race detector
	go test ./... -race -cover

lint: ## Run static analysis
	golangci-lint run

fmt: ## Format code
	gofmt -l -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Sync go.mod/go.sum
	go mod tidy

sqlc: ## Regenerate typed queries
	sqlc generate

migrate-up: ## Apply all migrations
	migrate -path db/migrations -database "$$DATABASE_URL" up

migrate-down: ## Roll back one migration
	migrate -path db/migrations -database "$$DATABASE_URL" down 1

docker-up: ## Start Postgres (and later the app)
	docker compose up -d

docker-down: ## Stop and remove containers
	docker compose down
```

| Maven phase | Make target here |
|---|---|
| `mvn clean` | (no equivalent — `go clean -cache` if needed) |
| `mvn compile` | `make build` |
| `mvn test` | `make test` |
| `mvn verify` / `checkstyle:check` | `make lint` |
| `spring-boot:run` | `make run` |
| `mvn dependency:tree` | `go mod graph` |

Note the `$$DATABASE_URL` — in a Makefile, `$` must be escaped as `$$` to reach the shell.

---

## 2.7 GoLand setup

You already have `.idea/` committed, so GoLand is your IDE. Worth configuring:

1. **GOROOT / Go SDK** — Settings → Go → GOROOT. Point at the Go 1.27 install.
2. **Actions on Save** — enable `gofmt` (GoLand calls it "Reformat code") and "Optimize imports". Now you never think about formatting.
3. **Run configurations** — create one of type *Go Build* with Run kind = *Package*, package = `learn101/cmd/api`, working directory = repo root. Working directory matters: `.env` is loaded relative to it.
4. **Environment variables** — in the run configuration, or just rely on `.env` (chapter 03).
5. **Database tool** — GoLand's DB panel can connect to the same Postgres you start in chapter 04. Handy for eyeballing `core_user` after a migration.
6. **Inspection** — Settings → Editor → Inspections → Go: leave the defaults, they're `go vet`-adjacent and accurate.

Useful GoLand shortcuts, if you're coming from IntelliJ muscle memory:

| Action | Shortcut |
|---|---|
| Go to file | `Ctrl+Shift+N` (same) |
| Go to symbol | `Ctrl+Alt+Shift+N` |
| Go to definition | `Ctrl+B` (same) |
| Find usages | `Alt+F7` (same) |
| Rename (refactor-safe) | `Shift+F6` (same) |
| Reformat | `Ctrl+Alt+L` (same) |
| Generate | `Alt+Insert` (same) |
| **Generate `String()`/constructor** | `Alt+Insert` → Generate |
| **Implement interface** | `Alt+Enter` on the type declaration |

GoLand's "Implement methods" (`Ctrl+I`) is how you satisfy an interface without typing the signatures — useful given there's no `implements` keyword to jump from.

---

## 2.8 Exercises

1. Run `go mod tidy` and read the resulting `go.mod` and `go.sum`. Note how much smaller the graph is than a Spring Boot `pom.xml`.
2. Run `go mod why github.com/joho/godotenv` and `go list -m all | wc -l`.
3. Apply the `.gitignore` above, then `git rm --cached learn101` and confirm `git status` shows the deletion.
4. Create the full directory skeleton with `mkdir -p cmd/api internal/{app,config,db,domain,repository,service,handler,middleware,auth} db/{migrations,queries}`.
5. Write the `Makefile` and run `make help`. Then `make fmt` and `make vet`.
6. Build for a target you don't own: `GOOS=linux GOARCH=arm64 go build -o /tmp/api-arm64 ./cmd/api`, then check `file /tmp/api-arm64`.

## 2.9 Done when

- [ ] `go mod tidy` succeeds and `go.sum` exists.
- [ ] The compiled binary is no longer tracked by git.
- [ ] `gofmt -l .` prints nothing.
- [ ] `go vet ./...` passes.
- [ ] `make help` lists your targets.
- [ ] You can explain what `internal/` buys you that a Java package name can't.

Next: [Chapter 03 — Configuration](03-configuration.md).
