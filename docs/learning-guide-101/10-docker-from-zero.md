# Chapter 10 — Docker from Zero

> **Goal:** understand what Docker actually is, what every command does, and the exact sequence of steps to containerise a service. No prior Docker experience assumed.
>
> **Read this before [Chapter 11](11-docker-and-graceful-shutdown.md).** Chapter 11 writes the production Dockerfile for this project and handles graceful shutdown. This chapter is the vocabulary and the mechanics that make chapter 11 readable.

If your DevOps team handles deployment, you've probably never needed to know this. But you own the `Dockerfile` — it's a build artifact of your codebase, like `go.mod`. Everything around it (registries, orchestration, TLS, scaling) can stay with them.

---

## 10.1 What problem Docker actually solves

You already know the pain from the Java side:

> "It works on my machine." — then it fails on the build server because the JDK version differs, or an env var is missing, or some library needs a system package that isn't installed.

The classic fix was a **virtual machine**: ship the whole OS. Reliable, but a VM is gigabytes, takes minutes to boot, and wastes most of its memory idling.

Docker's fix: ship the **application and its userland** — filesystem, libraries, config — but **share the host's kernel**. That gives you:

| | Virtual machine | Container |
|---|---|---|
| Shares | nothing; its own kernel | the host kernel |
| Size | GBs | MBs |
| Start time | minutes | milliseconds |
| Isolation | hardware-virtualised | kernel namespaces + cgroups |
| Contains | a full OS | your app + its dependencies only |
| Density | a handful per host | hundreds per host |

Two kernel features do the work, and it's worth knowing their names once:

- **Namespaces** — give each container its own view of the world: its own process list, its own network interfaces, its own filesystem root, its own hostname. That's why a container thinks it's the only thing running.
- **cgroups** — limit and account for resources: CPU, memory, I/O. That's how you cap a container at 512MB.

You don't configure either by hand. That's what the tooling is for. But when someone says "containers are just processes on the host", this is what they mean — and it explains almost every confusing behaviour in §10.9.

**The Java analogy that works:** a container image is like a JAR that also contains the JRE and a minimal OS. A running container is like `java -jar app.jar`, but with its own private filesystem and network. And Compose is a bit like a `docker-compose.yml` is to `docker run` what a `pom.xml` is to a giant command-line `javac` invocation.

---

## 10.2 The vocabulary, and the confusion it causes

Learn these eight words and 80% of Docker documentation becomes readable.

| Term | What it actually is | Java analogy |
|---|---|---|
| **Image** | A read-only template: a stack of filesystem layers plus metadata (entrypoint, env, ports). Never running. | A class, or a JAR |
| **Container** | A running (or stopped) instance of an image, with its own writable layer on top. | An object instance |
| **Layer** | The filesystem diff produced by one Dockerfile instruction. Shared between images. | A cached build step |
| **Dockerfile** | The recipe that builds an image. | `pom.xml` + build script |
| **Registry** | Where images are stored and pulled from. Docker Hub, GHCR, ECR, a private registry. | Maven Central |
| **Volume** | Storage that lives outside the container's lifecycle, so data survives. | A mounted data directory |
| **Network** | A private virtual network. Containers on the same one reach each other by service name. | A private subnet with DNS |
| **Compose** | A YAML file declaring multiple containers + networks + volumes, with one command to run them. | `docker-compose` = a mini orchestrator |

Two more pieces of software people conflate:

- **The Docker daemon** (`dockerd`) — the background service that actually builds images and runs containers. It runs as root.
- **The Docker CLI** (`docker`) — the client you type into. It talks to the daemon over a socket (`/var/run/docker.sock`).

So `docker build` is really "CLI sends the build context + Dockerfile to the daemon, daemon does the work". This matters in §10.6 when we talk about build context size.

**Docker Desktop vs Docker Engine:** Docker Desktop is a GUI + bundled daemon for macOS and Windows (where containers must run inside a lightweight Linux VM). On Linux you install Docker Engine directly. Either way, the commands are identical.

---

## 10.3 Install, verify, and fix the permission thing

**Linux:**

```bash
$ curl -fsSL https://get.docker.com | sh          # official convenience script
$ sudo usermod -aG docker "$USER"                 # stop needing sudo
$ newgrp docker                                   # or log out and back in
$ docker run --rm hello-world
```

If you skip the `usermod` line you'll hit this every time:

```
permission denied while trying to connect to the Docker daemon socket at
unix:///var/run/docker.sock
```

That's not a Docker bug — the socket is owned by the `docker` group, and your user isn't in it. **Adding yourself to that group is equivalent to giving yourself root**, which is why some shops make you use `sudo docker` instead. Know which policy your team uses.

**macOS / Windows:** install Docker Desktop, launch it, wait for the whale icon to stop animating.

Verify everything works:

```bash
$ docker version            # client AND server (daemon) — both must appear
$ docker info               # daemon details: storage driver, cgroup version, resources
$ docker run --rm hello-world
```

The three checks worth understanding:

- `docker version` printing only a *Client* section means the daemon isn't reachable.
- `docker info` failing with "Cannot connect to the Docker daemon" means the daemon isn't running (`sudo systemctl start docker` on Linux, launch Docker Desktop elsewhere).
- If `hello-world` runs, pull → create → start → log → remove all worked. That's the entire lifecycle in one command.

---

## 10.4 The commands, and the mental model behind them

Here's the single most useful thing to internalise: **`docker run` is really three commands in a trench coat.**

```
docker run nginx
   │
   ├── 1. PULL    fetch the image if it isn't cached locally
   ├── 2. CREATE  make a container from it (container is now "Created")
   └── 3. START   run it (container is now "Up", or "Exited" if it returned)
```

Once you see it that way, `docker ps` (running only), `docker ps -a` (including stopped), and "why did my container disappear?" all make sense.

### The core set

```bash
# --- images ---
docker pull postgres:16-alpine        # download an image
docker images                         # what's cached locally
docker image inspect postgres:16-alpine
docker rmi postgres:16-alpine         # remove a local image

# --- containers ---
docker run --rm hello-world                        # run, then delete on exit
docker run -d --name pg postgres:16-alpine         # detached (background)
docker run -it alpine sh                           # interactive shell
docker ps                                           # running containers
docker ps -a                                        # ALL containers, incl. stopped
docker logs -f --tail 100 pg                       # follow logs
docker exec -it pg sh                              # shell into a running container
docker stop pg                                     # SIGTERM, then SIGKILL after 10s
docker start pg                                    # restart a stopped container
docker rm pg                                       # delete a stopped container
docker rm -f pg                                    # force-delete a running one

# --- housekeeping ---
docker stats                        # live CPU/memory usage
docker system df                    # where disk space is going
docker system prune                 # remove stopped containers, dangling images
docker system prune -a              # ALSO remove unused images (be careful)
docker volume prune                 # unused volumes — this deletes data
```

### `docker run` flags that actually matter

```bash
docker run -d \
  --name learn101-db \
  -e POSTGRES_USER=learn101 \
  -e POSTGRES_PASSWORD=learn101 \
  -e POSTGRES_DB=learn101 \
  -p 5432:5432 \
  -v learn101-pgdata:/var/lib/postgresql/data \
  --restart unless-stopped \
  postgres:16-alpine
```

| Flag | Meaning | The part people get wrong |
|---|---|---|
| `-d` | Detached — run in the background | Without it, your terminal is attached and `Ctrl-C` kills the container |
| `--name` | A stable name instead of a random one like `lucid_euler` | Names must be unique; reuse requires `docker rm` first |
| `-e KEY=VAL` | Environment variable *inside* the container | This is how your 12-factor config arrives |
| `-p 5432:5432` | **host**`:`**container** | Read it as "expose container port 5432 on host port 5432". Swap them to your confusion. |
| `-v name:/path` | Mount a volume at a path in the container | The container path must be where the app *actually* writes |
| `--rm` | Delete the container when it exits | Great for one-off commands; **disastrous** with a volume-free database |
| `--restart unless-stopped` | Auto-restart on crash or reboot | The poor man's process supervisor |
| `-it` | Interactive + a TTY | Required for a shell |
| `--network` | Which network to join | Required for containers to talk to each other by name |

Try the Postgres example above, then poke at it:

```bash
$ docker run -d --name learn101-db \
    -e POSTGRES_PASSWORD=learn101 -e POSTGRES_USER=learn101 -e POSTGRES_DB=learn101 \
    -p 5432:5432 -v learn101-pgdata:/var/lib/postgresql/data \
    postgres:16-alpine

$ docker ps
$ docker logs --tail 20 learn101-db

# psql from INSIDE the container (no local psql needed):
$ docker exec -it learn101-db psql -U learn101 -d learn101 -c '\dt'

# ...or use your host psql, because we published the port:
$ psql "postgres://learn101:learn101@localhost:5432/learn101?sslmode=disable" -c 'select 1;'

$ docker stop learn101-db
$ docker start learn101-db             # data is still there — the volume
$ docker rm -f learn101-db             # data STILL there
$ docker volume rm learn101-pgdata     # NOW the data is gone
```

That sequence at the end is the single most important thing to understand about Docker: **container lifecycle and data lifecycle are separate.** Deleting the container does not delete the volume. This surprises people in both directions — "I deleted it and it's still there", and the far worse "`docker compose down -v` wiped my database".

---

## 10.5 Your first Dockerfile, line by line

A Dockerfile is a sequence of instructions, executed top to bottom, each producing a layer. Here's a deliberately naive one for a Go service:

```dockerfile
FROM golang:1.27-alpine

WORKDIR /app

COPY . .

RUN go build -o /app/api ./cmd/api

EXPOSE 8080

CMD ["/app/api"]
```

```bash
$ docker build -t learn101-api:naive .
$ docker run --rm -p 8080:8080 learn101-api:naive
```

It works. It's also ~400MB, because it ships the entire Go toolchain — and we'll fix that in §10.6 and chapter 11. For now, let's understand each instruction.

### The instructions

| Instruction | What it does | Notes |
|---|---|---|
| `FROM` | The base image to start from | Every Dockerfile must have one. `alpine` variants are tiny; `-slim` are middleweight. |
| `WORKDIR` | Sets the working directory for later instructions | Creates it if missing. Prefer it over `RUN cd`. |
| `COPY src dst` | Copies files **from your build context** into the image | |
| `ADD` | Like `COPY` but also unpacks tarballs and fetches URLs | Rarely what you want — use `COPY`. |
| `RUN cmd` | Executes during **build**, and the result is baked into a layer | Build-time only. |
| `CMD [...]` | The default command when the container **starts** | Runtime. Can be overridden. |
| `ENTRYPOINT [...]` | The fixed command for the container | Runtime. Harder to override. |
| `ENV K=V` | Environment variable, persisted into the image and runtime | |
| `ARG K` | Build-time-only variable | **Not** present at runtime. |
| `EXPOSE 8080` | Documents which port the app listens on | Purely documentation — it does **not** publish anything. `-p` does that. |
| `USER app` | Switch to a non-root user for subsequent instructions | Security. Do this. |
| `VOLUME /path` | Declares a mount point | Occasionally surprising; often omitted. |
| `HEALTHCHECK` | How to test whether the container is healthy | Needs a binary present in the image. |
| `LABEL` | Metadata (maintainer, version, source) | |
| `SHELL` | Override the default shell used by shell-form commands | |

`EXPOSE` catching you out is common: you add `EXPOSE 8080`, run without `-p`, and can't reach it. `EXPOSE` is a comment to humans. Publishing needs `-p 8080:8080`.

### `CMD` vs `ENTRYPOINT` — and why it matters for graceful shutdown

This one you must get right, because it's the difference between chapter 11's graceful shutdown working and not.

```dockerfile
# 1. Shell form — runs /bin/sh -c "/app/api"
CMD /app/api

# 2. Exec form — runs /app/api directly
CMD ["/app/api"]
```

Prefer **exec form**, always, for a server. With shell form, `/bin/sh` becomes PID 1 and your app is its child. Two consequences:

- **Signals aren't forwarded.** `docker stop` sends `SIGTERM` to PID 1, and `sh` does not pass it on, so your app never sees it and gets `SIGKILL` after the grace period. Your beautiful `signal.NotifyContext` handler never runs.
- Your app isn't PID 1, so it can't reap zombie processes.

| | Shell form | Exec form |
|---|---|---|
| Process tree | `sh` → your app | your app (PID 1) |
| Gets `SIGTERM` from `docker stop` | the shell does; not forwarded | your app does |
| Env var expansion (`$HOME`) | yes | no |
| Use it for | quick shell chains during debugging | **every server** |

`ENTRYPOINT` and `CMD` combine: `ENTRYPOINT` is the executable and `CMD` supplies default arguments. `docker run image --verbose` overrides `CMD` but not `ENTRYPOINT`. The common pattern for a binary:

```dockerfile
ENTRYPOINT ["/api"]
```

And if you need shell features *and* signal forwarding, use a proper init: `ENTRYPOINT ["/tini", "--", "/app/start.sh"]`, or `docker run --init`.

### Build context and `.dockerignore`

`docker build -t x .` — that trailing `.` is the **build context**: the entire directory tree sent to the daemon before the build starts. Not the Dockerfile's location, the *contents* it's allowed to `COPY`.

Consequences:

- A 2GB `.git` directory is uploaded (or at least tarred) on every build.
- `.env` files with real secrets can end up in a layer if you `COPY . .`.

`.dockerignore` fixes both. It's `.gitignore` for the build context:

```dockerignore
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

**An image layer is not private.** Anyone who can pull the image can read every file in it, forever. Never `COPY` a secret and never `ENV` one — pass it at runtime with `-e`/`--env-file` (or, in production, whatever your platform injects).

### Layer caching — why instruction order matters

Each instruction creates a layer, and Docker caches layers. A layer is reused if its instruction **and every layer above it** are unchanged. The moment one layer changes, everything below it is rebuilt.

So this is a mistake:

```dockerfile
COPY . .                              # any source edit invalidates everything below
RUN go mod download                   # re-downloads all dependencies every time
RUN go build -o /app/api ./cmd/api
```

And this is correct:

```dockerfile
COPY go.mod go.sum ./                 # changes rarely
RUN go mod download                   # cached until go.mod changes
COPY . .                              # changes often — invalidates only the build
RUN go build -o /app/api ./cmd/api
```

Same idea as Maven's `dependency:go-offline` in a separate layer. You'll see this ordering in chapter 11.

Practical consequence for the *order* of operations: put things that change rarely near the top (`FROM`, system packages, dependency manifests) and things that change constantly near the bottom (your source).

---

## 10.6 Multi-stage builds — the one concept that changes everything

The naive Dockerfile ships the compiler. At runtime you don't need the compiler, the Go toolchain, or even a shell — you need one static binary.

A **multi-stage build** uses several `FROM` instructions. Each `FROM` starts a fresh stage that can `COPY --from=` an earlier stage. Only the final stage becomes the image, so everything in the earlier stages is discarded.

```dockerfile
# ---------- stage 1: build (big, thrown away) ----------
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---------- stage 2: runtime (small, shipped) ----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
```

| | Naive | Multi-stage + distroless |
|---|---|---|
| Size | ~400MB | ~18MB |
| Contains a shell | yes | no |
| Contains the Go toolchain | yes | no |
| Attack surface | large | minimal |

Note `CGO_ENABLED=0`: it produces a **statically linked** binary with no libc dependency. That's the trick that makes `distroless/static` (or even `scratch`, which is literally empty) possible. Cross-compilation is the other half — `GOOS=linux GOARCH=amd64` — which is why you can build a Linux image from an Apple Silicon laptop.

The trade-off of distroless: no shell means no `docker exec -it ... sh`. Debugging needs a sidecar, a `-debug` variant image, or `docker debug`. Chapter 11 covers that trade-off in detail.

---

## 10.7 The main process: containerising this project, step by step

This is the part you asked about — the sequence. It is always the same nine steps.

```
1. .dockerignore          ─┐
2. Dockerfile              │  BUILD
3. docker build -t name . ─┘
                              │
4. docker run (with -e/-p/-v) │  RUN       ──►  verify: logs, curl, exec
                              │
5. add a database          ─┐
6. write docker-compose.yml │  COMPOSE
7. docker compose up -d --build ─┘         ──►  verify: /readyz, ps
                              │
8. docker tag + docker push   │  SHIP
                              │
9. DevOps pulls & runs it   ─┘  OPERATE
```

### Step 1 — Write `.dockerignore`

Before anything else. It determines what the daemon receives and prevents secrets from entering a layer. Copy the block from §10.5.

### Step 2 — Write the `Dockerfile`

Start naive, then make it multi-stage. For this project, chapter 11 has the final version. Verify you understand each line against §10.5 before moving on.

### Step 3 — Build

```bash
$ docker build -t learn101-api:dev .
$ docker images | grep learn101-api
```

What happens internally:

```
docker build .
   │
   ├─ read .dockerignore, tar the remaining build context, send to the daemon
   ├─ the daemon walks the Dockerfile
   ├─ each instruction: reuse a cached layer, or run it and cache the result
   └─ result: a tagged image, stored locally
```

**Step 3 is required after every source change.** This is the #1 beginner confusion: editing a `.go` file does nothing to a running container. The image is a snapshot. You must rebuild, then re-create the container.

Two gotchas:

- `-t learn101-api:dev` — the tag is a **label**, not a branch. Rebuilding with the same tag silently replaces the previous image and *orphans* it as a dangling `<none>` image if a container still references it. That's what `docker system prune` is for.
- Build cache is your friend, until it lies to you. If a build seems to ignore a change, `docker build --no-cache -t learn101-api:dev .` proves whether it's a caching problem.

### Step 4 — Run it and verify

```bash
$ docker run --rm \
    --name learn101-api \
    -p 8080:8080 \
    -e APP_ENV=production \
    -e HTTP_ADDR=:8080 \
    -e DATABASE_URL="postgres://learn101:learn101@host.docker.internal:5432/learn101?sslmode=disable" \
    -e JWT_SECRET="$(openssl rand -base64 48)" \
    learn101-api:dev

$ docker logs -f learn101-api
$ curl -s localhost:8080/healthz
```

Note `host.docker.internal` — see §10.8. On Linux you need `--add-host=host.docker.internal:host-gateway`.

### Step 5 — Add the database

You now have two things that need to talk to each other. You *could* keep going with `docker run`:

```bash
$ docker network create learn101-net

$ docker run -d --name learn101-db --network learn101-net \
    -e POSTGRES_USER=learn101 -e POSTGRES_PASSWORD=learn101 -e POSTGRES_DB=learn101 \
    -v learn101-pgdata:/var/lib/postgresql/data postgres:16-alpine

$ docker run --rm --network learn101-net -p 8080:8080 \
    -e DATABASE_URL="postgres://learn101:learn101@learn101-db:5432/learn101?sslmode=disable" \
    -e JWT_SECRET="$(openssl rand -base64 48)" \
    learn101-api:dev
```

Note `@learn101-db:5432` — on a user-defined network, **the container name is the hostname**. That's the mechanism Compose later does for you.

This works. It's also unusable: nobody can reproduce it, and it's three commands that must run in a specific order. Which is why Compose exists.

### Step 6 — Write `docker-compose.yml`

Compose turns that command soup into a declarative file. **Your existing Postgres from chapter 04 is already Compose** — go re-read it now with §10.5's vocabulary; it'll read differently.

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: learn101
      POSTGRES_PASSWORD: learn101
      POSTGRES_DB: learn101
    ports:
      - "5432:5432"                       # host:container, same as -p
    volumes:
      - postgres-data:/var/lib/postgresql/data   # named volume, same as -v
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U learn101 -d learn101"]
      interval: 5s
      timeout: 5s
      retries: 10

volumes:
  postgres-data:
```

The translation from `docker run` flags is exact, which is the fastest way to learn Compose:

| `docker run` | Compose key |
|---|---|
| `--name pg` | the service name (`postgres:`) |
| `-d` | `docker compose up -d` |
| `-e K=V` | `environment:` map |
| `--env-file .env` | `env_file:` list |
| `-p 5432:5432` | `ports:` list |
| `-v name:/path` | `volumes:` list |
| `--network net` | implicit — Compose makes one network per project |
| `--restart unless-stopped` | `restart: unless-stopped` |
| `--rm` | not needed — `docker compose down` cleans up |
| image `postgres:16-alpine` | `image:` |
| your own build | `build:` (context + dockerfile) |

### Step 7 — Run it

```bash
$ docker compose up -d --build      # build images, then start everything
$ docker compose ps
$ docker compose logs -f api
$ docker compose exec postgres psql -U learn101 -d learn101
$ docker compose exec api sh        # if your image has a shell

$ docker compose stop api           # SIGTERM, drain
$ docker compose restart api
$ docker compose down               # stop + remove containers/networks, KEEP volumes
$ docker compose down -v            # ...and DESTROY volumes — your database is gone
$ docker compose up -d --force-recreate
```

`--build` is needed whenever the Dockerfile or your source changed. `docker compose up -d` alone reuses the existing image, which is the second most common beginner confusion. Chapter 11 wires `--build` and dependency ordering (`depends_on` with `condition`) properly.

### Step 8 — Tag and push to a registry

An image on your laptop is useless to your DevOps team. Images travel through a registry.

```bash
$ docker build -t learn101-api:$(git rev-parse --short HEAD) .
$ docker tag learn101-api:abc1234 ghcr.io/your-org/learn101-api:abc1234
$ docker tag learn101-api:abc1234 ghcr.io/your-org/learn101-api:latest

$ echo "$GITHUB_TOKEN" | docker login ghcr.io -u YOUR_USER --password-stdin
$ docker push ghcr.io/your-org/learn101-api:abc1234
$ docker push ghcr.io/your-org/learn101-api:latest
```

A full image reference is `registry/namespace/name:tag`:

```
ghcr.io        / your-org / learn101-api : abc1234
   │               │            │             │
registry      namespace       image          tag
```

**Tag hygiene matters and is worth an opinion:** tag with the git SHA, treat `<sha>` as immutable, and be skeptical of `latest`. `latest` is just a default tag — nothing enforces that it points at your newest build, and if it silently changes, "deploy the same thing again" stops meaning anything. Use it as a convenience alias, never as the artefact your deploy references.

### Step 9 — The handoff to DevOps

What you give them, and what they do with it, is covered in §10.11. The short version: they need an image, a port, and a documented list of environment variables. You need not touch Kubernetes.

---

## 10.8 Networking and the `localhost` trap

This causes more beginner confusion than anything else in Docker. Read it twice.

**A container has its own network namespace and its own loopback interface.** So:

- `localhost` **inside** a container means *that container*, not your laptop.
- A service on your laptop is **not** reachable at `localhost` from inside a container.
- Conversely, publishing a port with `-p 8080:8080` makes the container reachable from your laptop at `localhost:8080`.

```
   Your laptop                          Container
   ┌──────────────┐                     ┌──────────────┐
   │              │                     │              │
   │  localhost   │◄──── -p 8080:8080 ──┤ :8080  app   │
   │   :8080      │     (published)     │              │
   │              │                     │  localhost   │  ← this is the CONTAINER,
   │  postgres    │                     │  :5432       │    not your laptop
   │   :5432      │◄──── ? ─────────────┤              │
   └──────────────┘                     └──────────────┘
                  ^
                  └── you MUST publish (or share a network).
                      host.docker.internal is the macOS/Windows escape hatch.
```

| You want to connect to | From | Address to use |
|---|---|---|
| Your app inside a container | your laptop | `localhost:8080` (because of `-p 8080:8080`) |
| A DB on your laptop | from inside a container | `host.docker.internal:5432` (Docker Desktop, or `--add-host=host.docker.internal:host-gateway` on Linux) |
| Another container | from inside a container | the **container/service name**: `learn101-db:5432` |
| Your container | from another container | same network + service name |

Why the service name works: Compose creates a user-defined bridge network per project and runs an embedded DNS server on it, resolving each service name to its container IP. That's why chapter 11's `DATABASE_URL` uses `postgres` as the host and *not* `localhost`.

And the corollary that bites everyone: **a container's hostname is not always its Compose service name.** Inside a Compose project, use the service name. Between plain `docker run` containers, use `--name` on a shared `--network`.

---

## 10.9 Volumes: where your data actually lives

A container's writable layer is **deleted with the container**. Anything your app writes there is gone the moment you `docker rm` it. If you rebuild and recreate a database container, you lose the database.

Volumes are how you opt out of that.

| Type | Syntax | Where it lives | Use for |
|---|---|---|---|
| **Named volume** | `-v learn101-pgdata:/var/lib/postgresql/data` | Docker-managed area (`/var/lib/docker/volumes/…`) | Databases, anything Docker should own |
| **Bind mount** | `-v $(pwd)/data:/var/lib/postgresql/data` | A path you choose on the host | Reading source/config from the host |
| **tmpfs** | `--tmpfs /tmp` | Memory only | Scratch data |

```bash
$ docker volume ls
$ docker volume inspect learn101-pgdata     # shows the Mountpoint path
$ docker volume rm learn101-pgdata          # the only command that truly deletes the data
```

| Command | Containers | Networks | Volumes |
|---|---|---|---|
| `docker stop x` | kept | kept | kept |
| `docker rm x` | **gone** | kept | kept |
| `docker compose down` | gone | gone | **kept** |
| `docker compose down -v` | gone | gone | **GONE** |
| `docker system prune` | unused gone | unused gone | kept |
| `docker volume prune` | — | — | unused **GONE** |

Memorise the `down` vs `down -v` row. It is the most common way to accidentally delete a development database, and it's exactly the "reset the schema" button chapter 04 mentioned — useful when you mean it, catastrophic when you don't.

A practical habit: never keep the only copy of anything in a volume. Your migrations + seed data should be enough to rebuild from scratch — and chapter 04's `migrate up` workflow exists precisely so that "wipe and rebuild" is a two-command recovery rather than a crisis.

---

## 10.10 Debugging: the five questions

Docker's failure modes are learnable because they repeat. Ask these in order.

**1. Is it running?**

```bash
$ docker ps            # nothing? then:
$ docker ps -a         # look at the STATUS column and the exit code
```

A container that "instantly disappeared" almost always ran a command that finished or crashed. `docker ps -a` shows `Exited (1) 3 seconds ago`. Which brings us to:

**2. Why did it exit?**

```bash
$ docker logs learn101-api
$ docker inspect learn101-api --format '{{.State.ExitCode}} {{.State.Error}}'
```

| Exit reason | Typical cause |
|---|---|
| `Exited (0)` immediately | The command wasn't a long-running process — e.g. `CMD /app/api` where the binary printed help and returned. A server must *block*. |
| `Exited (1)` with a config error | Your fail-fast `config.Load` working correctly — `DATABASE_URL is required` |
| `Exited (1)` with `connection refused` | `DATABASE_URL` points at `localhost` from inside the container (see §10.8) |
| `Exited (127)` | Command not found — wrong path in `CMD`, or a binary built for another OS/arch |
| `Exited (137)` | Killed — OOM, or `docker stop` ran out its grace period and escalated to `SIGKILL` |
| `Exited (139)` | SIGSEGV |
| `Exited (143)` | Terminated by `SIGTERM` — expected during `docker stop` |

**3. What does the app say?**

`docker logs` only captures **stdout and stderr**. If your app writes to a file inside the container, you will see nothing. This is why chapter 08 logs JSON to stdout and nowhere else — it's not just a Go convention, it's the container contract.

```bash
$ docker logs -f --tail 50 learn101-api
$ docker compose logs -f --tail 50 api
```

**4. Can I get a shell in there?**

```bash
$ docker exec -it learn101-api sh
$ docker compose exec postgres psql -U learn101 -d learn101
```

If that fails with `executable file not found`, your image has no shell — a distroless or `scratch` base. That's a deliberate trade-off (chapter 11), and the fix is a debug variant image or `docker debug`.

**5. Is it a networking/port problem?**

```bash
$ docker port learn101-api            # what's published
$ curl -v localhost:8080/healthz
$ docker run --rm --network container:learn101-api nicolaka/netshoot curl -s localhost:8080/healthz
```

### The errors you will actually hit

| Error message | Cause | Fix |
|---|---|---|
| `Cannot connect to the Docker daemon` | Daemon not running / not reachable | Start Docker Desktop, or `sudo systemctl start docker` |
| `permission denied while trying to connect to the Docker daemon socket` | Your user isn't in the `docker` group | `sudo usermod -aG docker $USER`, then re-login |
| `Bind for 0.0.0.0:5432 failed: port is already allocated` | Something else holds that host port — often your local Postgres | Change the host side: `-p 5433:5432`, or stop the other process |
| `Conflict. The container name "/x" is already in use` | A stale container with that name exists (even stopped) | `docker rm x` |
| `no matching manifest for linux/arm64/v8` | The image has no build for your CPU architecture | `--platform linux/amd64` (Apple Silicon pulling an x86-only image) |
| `exec format error` | Binary built for a different OS/arch than the image | `GOOS=linux GOARCH=amd64` when building |
| `COPY failed: file not found in build context` | The file is excluded by `.dockerignore`, or the path is relative to the context, not the Dockerfile | Fix `.dockerignore` or the path |
| `docker compose up` doesn't reflect my code change | No rebuild | `docker compose up -d --build` |
| Container runs fine but `curl` fails | Port not published, or app binds `127.0.0.1` inside the container | `-p`, and bind `0.0.0.0:8080` |
| Disk full, builds failing | Accumulated images/volumes | `docker system df`, then `docker system prune` |

That last one deserves a warning: `docker system prune -a` and `docker volume prune` **delete things you may want**, including the local image cache and any volume not currently attached to a container. Read the prompt before typing `y`.

---

## 10.11 Who owns what after the handoff

Since your DevOps team runs deployment, this boundary is worth making explicit — because the seam between "your image" and "their platform" is where production incidents come from.

| You own | They own |
|---|---|
| `Dockerfile` and `.dockerignore` | The registry and its retention policy |
| The build: `docker build`, tag with the git SHA | CI pipeline, image scanning, signing |
| Which port the app listens on | Load balancer, ingress, TLS certificates |
| The **contract**: env var names, `/healthz`, `/readyz` | Probes, orchestration, restart policy, scaling |
| Graceful `SIGTERM` handling (chapter 11) | When a rolling deploy sends `SIGTERM` and how long it waits |
| Logging to stdout as JSON | Log aggregation, retention, dashboards |
| No secrets baked into the image | Secret injection, rotation |

**The contract to agree on, in writing:**

1. **Config comes from environment variables only** — never a config file baked into the image. Chapter 03's `config.Load` is exactly this.
2. **Two health endpoints, with distinct meanings.** `/healthz` = "the process is alive" (liveness), `/readyz` = "I can serve traffic, the DB answers" (readiness). Chapter 08 built both; a readiness probe that checks the database is what stops traffic routing to a replica during a brief DB blip.
3. **A single documented port.** `HTTP_ADDR=:8080`, and it must bind `0.0.0.0`, not `127.0.0.1`, or nothing outside the container can reach it.
4. **`SIGTERM` handled within a stated deadline.** Chapter 11's shutdown takes up to 15s, so `stop_grace_period` / `terminationGracePeriodSeconds` must exceed that.
5. **Logs to stdout/stderr, structured.** Chapter 08.
6. **The image is immutable and contains no secrets.** Everything sensitive arrives at runtime.

Ask them: *which* registry, *which* environment variables must be set in production, what the health probe paths should be, and what grace period they allow on shutdown. Those four answers determine your Dockerfile and your `main.go`.

---

## 10.12 Security basics worth knowing now

You don't need to be a security engineer, but these are cheap and expected of the person writing the Dockerfile:

- **Don't run as root.** Add a `USER` instruction. `distroless:nonroot` does it for you; on `alpine` use `RUN adduser -D -u 10001 app`.
- **Keep the image minimal.** Fewer binaries means fewer CVEs, and no shell means an attacker who gets code execution has far less to work with.
- **Never bake in a secret.** Not in `ENV`, not in `COPY . .` (check `.dockerignore`), not in `ARG` — build args are visible in image history.
- **Pin what you depend on.** `postgres:16-alpine` is better than `postgres:latest`; a digest (`postgres:16-alpine@sha256:...`) is better still, because tags are mutable.
- **Scan the image.** `trivy image learn101-api:dev`, or whatever your CI already runs. Fix the criticals.
- **`.dockerignore` the `.env`.** The habit matters more than the file.
- **Know that `-p 5432:5432` on a laptop is fine and on a server is not.** Published ports bypass any firewall on some hosts.

---

## 10.13 Command cheat sheet

```bash
# ── images ────────────────────────────────────────────────────────────
docker pull IMAGE:TAG                 # download
docker images                         # list local
docker build -t NAME:TAG .            # build from Dockerfile in .
docker build --no-cache -t NAME:TAG . # ignore the layer cache
docker tag SRC DST                    # add another name to an image
docker push IMAGE:TAG                 # upload to a registry
docker rmi IMAGE:TAG                  # delete locally
docker history IMAGE:TAG              # see the layers
docker image inspect IMAGE:TAG        # metadata incl. ENTRYPOINT/ENV

# ── containers ───────────────────────────────────────────────────────
docker run -d --name N -p H:C -e K=V -v VOL:PATH IMAGE:TAG
docker run -it --rm IMAGE:TAG sh      # throwaway shell
docker ps                             # running
docker ps -a                          # all, with exit codes
docker logs -f --tail 100 N           # follow stdout/stderr
docker exec -it N sh                  # shell into a running container
docker inspect N                      # full JSON state
docker stats                          # live resource usage
docker port N                         # published ports
docker stop N                         # SIGTERM, then SIGKILL after 10s
docker start N / docker restart N
docker rm N                           # delete a stopped container
docker rm -f N                        # force
docker cp N:/path ./local             # copy files out

# ── volumes ──────────────────────────────────────────────────────────
docker volume ls
docker volume inspect VOL
docker volume rm VOL
docker volume prune

# ── networks ─────────────────────────────────────────────────────────
docker network ls
docker network create NET
docker network inspect NET

# ── compose ──────────────────────────────────────────────────────────
docker compose up -d                  # start in background
docker compose up -d --build          # rebuild images first
docker compose ps
docker compose logs -f --tail 100 api
docker compose exec postgres psql -U learn101 -d learn101
docker compose stop api               # graceful stop of one service
docker compose restart api
docker compose down                   # remove containers + network, keep volumes
docker compose down -v                # ALSO delete volumes  ← destroys data
docker compose config                 # print the resolved file (great for debugging)

# ── housekeeping ─────────────────────────────────────────────────────
docker system df                      # disk usage breakdown
docker system prune                   # stopped containers, unused networks, dangling images
docker system prune -a                # also unused images  ← read the warning
docker system prune --volumes         # also unused volumes   ← deletes data
```

---

## 10.14 Glossary

| Term | One-line definition |
|---|---|
| **Dockerfile** | The recipe for building an image; a list of instructions, one layer each. |
| **Image** | An immutable, layered filesystem + metadata. Not running. |
| **Container** | A running instance of an image, plus its own writable layer and namespaces. |
| **Layer** | The filesystem diff produced by a single Dockerfile instruction. Shared, cached, read-only. |
| **Build context** | The directory tree sent to the daemon by `docker build`; filter it with `.dockerignore`. |
| **Registry** | Storage for images (Docker Hub, GHCR, ECR). `docker push` / `docker pull`. |
| **Tag** | A mutable label on an image, e.g. `:16-alpine` or `:abc1234`. |
| **Digest** | An immutable content hash, `@sha256:…`. The trustworthy way to reference an image. |
| **Named volume** | Docker-managed persistent storage; survives container deletion. |
| **Bind mount** | A host directory mapped into the container; the host owns it. |
| **Bridge network** | The default virtual network giving containers IPs and (user-defined) DNS. |
| **Compose** | A YAML file plus a CLI for running multi-container setups. |
| **Multi-stage build** | Several `FROM` stages, where only the last ships; the rest are thrown away. |
| **Distroless** | A base image with your binary and almost nothing else — no shell, no package manager. |
| **PID 1** | The first process in a container. Receives signals. Your app should be it. |
| **`docker stop`** | Sends `SIGTERM`, waits (10s default), then `SIGKILL`. |
| **Orchestrator** | Software that runs containers across many machines — Kubernetes, ECS, Nomad. Your DevOps team's domain. |

---

## 10.15 Exercises

1. `docker run --rm hello-world` and `docker system df`. Then find the `hello-world` image in `docker images` and remove it.
2. Run the Postgres container from §10.4. Connect with `docker exec -it ... psql`, create a table, insert a row. Then `docker rm -f` the container, start a new one from the same image with the same `-v` volume, and confirm the row survived. Now `docker volume rm` it and watch the data disappear. This exercise is the whole point of §10.9.
3. Break the port mapping: run Postgres with `-p 5433:5432`. Confirm your host `psql` now needs port 5433 while the container is unchanged.
4. Run an `alpine` container that exits immediately (`docker run alpine echo hi`), then `docker ps -a` and read the exit code. Then run `docker run alpine` with no command and explain the exit code from the docs.
5. Build the naive Dockerfile from §10.5. `docker images` and note the size. Run `docker history learn101-api:naive` and find the layer that contains the Go toolchain. Then build the multi-stage version and compare sizes.
6. Break the build cache: change a line in `main.go`, rebuild, and watch which layers are `CACHED` and which run. Then move `COPY . .` above `RUN go mod download` and observe the difference in a clean build.
7. Make a container fail on purpose by setting `DATABASE_URL` to `localhost`, and diagnose it using only `docker logs` and the `Exited (N)` code. Then fix it with `host.docker.internal`.
8. `docker compose down -v` your Postgres, then `docker compose up -d` and re-apply migrations with `migrate ... up`. Time how long recovery takes — this is why the wipe-and-rebuild workflow is safe to have.
9. Push an image somewhere real: create a free Docker Hub repo (or GHCR), `docker tag`, `docker login`, `docker push`. Then `docker rmi` it locally and `docker pull` it back. That round trip is what your DevOps team does.
10. Write down, for your own project, the six contract items from §10.11 with concrete values. That document is what you hand to DevOps.

## 10.16 Done when

- [ ] You can explain the difference between an image and a container without hesitating.
- [ ] You can explain why `localhost` inside a container isn't your laptop, and what `host.docker.internal` is for.
- [ ] You know which of `docker rm`, `docker compose down` and `docker compose down -v` destroy data.
- [ ] You have built an image, run it, read its logs, and got a shell inside a running container.
- [ ] You have pushed an image to a registry and pulled it back.
- [ ] You know why `CMD ["/app/api"]` is written in exec form.
- [ ] You can name the three things `docker run` does internally.
- [ ] You have written down the six contract items for your own project.

Next: [Chapter 11 — Docker and Graceful Shutdown](11-docker-and-graceful-shutdown.md), where this all gets applied to the actual API — the production Dockerfile, Compose with the database wired in, and shutdown that doesn't drop requests.
