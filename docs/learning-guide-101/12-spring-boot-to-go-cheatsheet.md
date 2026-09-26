# Chapter 12 — Spring Boot → Go Cheat Sheet

> **Goal:** a reference you'll come back to. When a Java reflex fires, find it here.

---

## 12.1 Annotations → Go idioms

There is no annotation processing in Go. Every row below is something Spring does for you via reflection and that Go makes you write as plain code.

### Application & wiring

| Spring Boot | Go |
|---|---|
| `@SpringBootApplication` | `package main` + `func main()` |
| `@ComponentScan` | import the package |
| `@Configuration` class | `internal/app/app.go` with a `Build(...)` function |
| `@Bean` | a function returning the value, called during wiring |
| `@Autowired` (field) | a struct field set by a constructor function |
| `@Autowired` (constructor) + `@RequiredArgsConstructor` | `func NewX(depA A, depB B) *X` |
| `@Qualifier` | name your constructors differently (`NewRedisCache`, `NewMemoryCache`) |
| `@Primary` | just pass the one you want |
| `@Lazy` | pass a `func() *T`, or construct later |
| `@Value("${x}")` | `cfg.X` from `config.Load()` |
| `@ConfigurationProperties` | a plain struct |
| `@Profile("dev")` | `if cfg.IsDevelopment() { ... }` |
| `@ConditionalOnProperty` | an `if` |
| `@PostConstruct` | the last lines of `NewX`, or an explicit `Start()` |
| `@PreDestroy` | `defer` in `run()` |
| `@Scope("prototype")` | call the constructor; Go values are not singletons by default |
| `@Order` on filters | the order of `r.Use(...)` calls |
| `Environment.getProperty()` | nothing — pass `cfg` around |
| `ApplicationContext` | `app.Build(...)`'s return value, or just local variables |
| `@EnableAsync`, `@EnableScheduling` | `go func()`, `time.Ticker` |

### Web layer

| Spring MVC | Gin |
|---|---|
| `@RestController` | a struct with methods + `NewXHandler(...)` |
| `@Controller` (view) | not applicable — JSON APIs only here |
| `@RequestMapping("/api/v1")` | `r.Group("/api/v1")` |
| `@GetMapping("/users/{uuid}")` | `v1.GET("/users/:uuid", h.Get)` |
| `@PostMapping` | `v1.POST(...)` |
| `@RequestBody` | `c.ShouldBindJSON(&req)` |
| `@Valid` | `binding:"..."` tags on the DTO |
| `@RequestParam` | `c.Query("page")` |
| `@PathVariable` | `c.Param("uuid")` |
| `@RequestHeader` | `c.GetHeader("Authorization")` |
| `ResponseEntity<T>` | `c.JSON(status, body)` |
| `@ResponseStatus` | the status you pass to `c.JSON` |
| `@ControllerAdvice` | `respondError` (chapter 08) |
| `@ExceptionHandler` | a branch in the error-mapping table |
| `@RestControllerAdvice` + `@ResponseBody` | the same |
| `Filter` | `gin.HandlerFunc` middleware |
| `HandlerInterceptor` | `gin.HandlerFunc` middleware |
| `OncePerRequestFilter` | `c.Next()` runs once per request by construction |
| `CorsFilter` / `@CrossOrigin` | `gin-contrib/cors` |
| `@SessionAttributes` | not used — stateless API |
| `@Transactional` (on service) | `pool.Begin(ctx)` explicitly (chapter 05) |
| `@ResponseBody` | the default — `c.JSON` is explicit |
| `ModelAndView` | not used |
| Jackson (`ObjectMapper`) | `encoding/json` (Gin wraps it) |
| `@JsonProperty` | `json:"field_name"` struct tag |
| `@JsonIgnore` | `json:"-"` |
| `@JsonInclude(NON_NULL)` | `json:"x,omitempty"` |
| `@DateTimeFormat` | `time.Time` handles RFC3339; custom formats need a custom type |
| `HttpServletRequest` | `c.Request` |
| `HttpServletResponse` | `c.Writer` |
| `Principal` / `Authentication` | `c.Get(middleware.ContextUserID)` |

### Persistence

| Spring Data JPA / Hibernate | Go + sqlc |
|---|---|
| `@Entity` | a struct in `internal/db/models.go` (generated) |
| `@Table(name="core_user")` | the SQL in your migration |
| `@Id` + `@GeneratedValue` | `bigserial` / `RETURNING *` |
| `@Column(nullable=false)` | `NOT NULL` in SQL |
| `@OneToMany` / `@ManyToOne` | a `JOIN` in a query, or two queries |
| `@JoinColumn` | a foreign key in SQL |
| `@Enumerated` | a `CHECK` constraint or a Postgres enum |
| `@Version` (optimistic locking) | a `version` column and `WHERE version = $n` manually |
| `@CreatedDate` / `@LastModifiedDate` | `now()` in SQL, or a trigger |
| `@Transactional` | `pgx.Tx` (explicit) |
| `JpaRepository<T, ID>` | generated functions in `internal/db` |
| `findById` | `GetUserByID` |
| `findAll` | `ListUsers` |
| `save` | `CreateUser` / `UpdateUser` (`RETURNING *`) |
| `deleteById` | `SoftDeleteUser` (this project never hard-deletes) |
| `count()` | `CountUsers` |
| `existsByEmail` | `count(*) WHERE email` or `GetUserByEmail` + `errors.Is` |
| `@Query("...")` | `db/queries/*.sql` |
| Derived queries (`findByEmailAndStatusNot`) | write the SQL |
| `@Modifying` | an `UPDATE`/`DELETE` query with `:exec`/`:execrows` |
| `saveAndFlush` | not needed — `RETURNING *` gives you the row |
| `EntityManager` | `pgxpool.Pool` |
| First-level cache | none — every call hits the DB |
| Lazy loading | none — you select what you need |
| `LazyInitializationException` | impossible |
| N+1 queries | your choice; write a `JOIN` |
| Flyway / Liquibase | golang-migrate |
| HikariCP | `pgxpool` |
| `spring.datasource.url` | `DATABASE_URL` |

### Testing

| JUnit / Spring Test | Go |
|---|---|
| `@Test` | `func TestXxx(t *testing.T)` |
| `@BeforeEach` | a setup call, or `t.Cleanup` |
| `@AfterEach` | `t.Cleanup(func(){...})` |
| `@BeforeAll` | `TestMain(m *testing.M)` |
| `@Nested` | `t.Run("name", func(t *testing.T){...})` |
| `@ParameterizedTest` | table-driven: a slice + a loop |
| `@Disabled` | `t.Skip("reason")` |
| `assertThat(x).isEqualTo(y)` | `assert.Equal(t, y, x)` (testify) |
| `assertThrows(FooException.class, ...)` | `assert.ErrorIs(t, err, domain.ErrFoo)` |
| `@Mock` / `Mockito.mock` | a hand-written struct implementing an interface |
| `when(...).thenReturn(...)` | set a field on your fake |
| `when(...).thenThrow(...)` | `store.ErrOnCreate = err` |
| `verify(mock).method()` | assert on the fake's recorded state, if you must |
| `@InjectMocks` | `NewService(fakeA, fakeB)` |
| `@SpringBootTest` | `app.Build(cfg, pool, log)` + `httptest` |
| `@WebMvcTest` + `MockMvc` | `httptest.NewRecorder()` + `r.ServeHTTP` |
| `@DataJpaTest` | testcontainers Postgres + sqlc, build-tagged |
| `@Sql` | run migrations, then insert with your own queries |
| `@DirtiesContext` | unnecessary — no shared context |
| `@ActiveProfiles("test")` | `t.Setenv(...)` |
| `@TestPropertySource` | `t.Setenv(...)` |
| Surefire (unit) / Failsafe (IT) | `*.go` tests vs `//go:build integration` |
| `@WithMockUser` | inject identity via a middleware in the test router |
| JaCoCo | `go test -coverprofile` + `go tool cover` |
| Mockito inline mock maker | not applicable |

---

## 12.2 Dependency equivalents

The `go.mod` you'll end up with:

```
module learn101

go 1.27

require (
	github.com/gin-contrib/cors v1.7.6
	github.com/gin-gonic/gin v1.11.0
	github.com/go-playground/validator/v10 v10.27.0
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.8.0
	github.com/joho/godotenv v1.5.1
	github.com/stretchr/testify v1.11.1
	golang.org/x/crypto v0.43.0
)

require (
	// test-only
	github.com/golang-migrate/migrate/v4 v4.18.3
	github.com/testcontainers/testcontainers-go v0.39.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.39.0
)

require ( /* indirect: pulled in by the above */ )
```

Install them:

```bash
$ go get github.com/gin-gonic/gin@latest
$ go get github.com/gin-contrib/cors@latest
$ go get github.com/go-playground/validator/v10@latest
$ go get github.com/golang-jwt/jwt/v5@latest
$ go get github.com/google/uuid@latest
$ go get github.com/jackc/pgx/v5@latest
$ go get github.com/joho/godotenv@latest
$ go get github.com/stretchr/testify@latest
$ go get golang.org/x/crypto@latest
$ go get -t github.com/golang-migrate/migrate/v4@latest
$ go get -t github.com/testcontainers/testcontainers-go/modules/postgres@latest
$ go mod tidy
```

Version numbers move; `@latest` is fine. Note there are no "BOM" or alignment concerns — MVS resolves one version per module.

| Spring / Java ecosystem | Go | Notes |
|---|---|---|
| Spring Web MVC | `gin-gonic/gin` | or `chi`, or stdlib `net/http` |
| Jackson | `encoding/json` | stdlib; struct tags replace annotations |
| Bean Validation (`@Valid`) | `go-playground/validator/v10` | transitive dep of Gin |
| Spring Security | hand-rolled middleware + `golang-jwt/jwt/v5` | chapter 07 |
| BCrypt (`BCryptPasswordEncoder`) | `golang.org/x/crypto/bcrypt` | |
| Hibernate / JPA | **sqlc** | codegen, not a runtime |
| HikariCP | `pgxpool` (in `jackc/pgx/v5`) | |
| Flyway / Liquibase | `golang-migrate/migrate` | |
| JUnit 5 | `testing` (stdlib) | no dependency |
| Mockito | **hand-written fakes** | no dependency |
| AssertJ | `stretchr/testify` | optional convenience |
| Testcontainers (Java) | `testcontainers-go` | same company, same idea |
| Logback / SLF4J | `log/slog` (stdlib) | chapter 08 |
| Spring Actuator | hand-written `/healthz`, `/readyz` | |
| Micrometer | `prometheus/client_golang` | |
| OpenTelemetry SDK | `go.opentelemetry.io/otel` | |
| Lombok | **nothing** | no boilerplate to remove |
| MapStruct | `sqlc` (for DB), hand-written mappers (for DTOs) | |
| `spring-boot-devtools` | `air` / `go run` | |
| Maven / Gradle | the `go` command + a `Makefile` | |
| `RestTemplate` / `WebClient` | stdlib `net/http` or `resty` | |
| `@Scheduled` / Quartz | `time.Ticker` or `robfig/cron/v3` | |
| `@Async` / `ExecutorService` | `go func()` + `sync.WaitGroup` | |
| `CompletableFuture` | channels, or `errgroup.Group` | |
| `@Retryable` | a `for` loop, or `cenkalti/backoff` | |
| `@Cacheable` | explicit code, or `ristretto`/`bigcache` | |
| Vault / Spring Cloud Config | env vars from your platform | |

---

## 12.3 Things Go has no equivalent for

These are the concepts you must **stop reaching for**. There's no workaround because the Go community considers them the wrong shape.

| Spring concept | Why there's no Go answer | What to do instead |
|---|---|---|
| **Annotation-driven DI container** | reflection-based wiring is considered opaque and startup-fragile | write `app.Build(...)` by hand (~20 lines) |
| **`@Transactional` proxy magic** | AOP via proxies hides control flow and breaks on self-invocation | `pool.Begin(ctx)` + `defer tx.Rollback(ctx)` |
| **AOP / aspects** | no bytecode manipulation, no proxies | a middleware, a decorator function, or call the code |
| **Lazy loading** | implicit queries are the #1 source of production surprises | select exactly what you need |
| **Dirty checking / unit of work** | implicit writes are worse than implicit reads | call the `UPDATE` |
| **`@Entity` lifecycle callbacks** | no ORM, so no callbacks | DB triggers, or explicit statements |
| **Runtime property refresh** | immutable config is a feature | restart the process |
| **Profiles as first-class config** | too much implicit behaviour | one `APP_ENV` field and explicit `if`s |
| **WAR deployment on an app server** | Go binaries are self-contained | a container, or systemd |
| **`ThreadLocal` / MDC** | no thread-local storage in the same way; no MDC | pass a `*slog.Logger` with fields, or context values |
| **Field injection** | encourages hidden dependencies | constructor injection only |
| **Explicit `interface` implementation** | structural typing means it's implicit | just write the methods |
| **Inheritance / abstract classes** | composition only | embedding + interfaces |
| **Exceptions** | control flow you can't see in the signature | `if err != nil`, always |
| **`@Nullable` / `Optional` everywhere** | zero values are the default | `*T` when absence is meaningful |
| **Checked exceptions** | no equivalent at all | conventions + linters |
| **`synchronized`** | no intrinsic locks on objects | `sync.Mutex` as a field, never copied |
| **Getters/setters** | not idiomatic | exported fields, or a method when it does work |
| **`toString()` for everything** | not idiomatic | `String()` only when it aids debugging |
| **Package-level access modifiers** | `internal/` is directory-level only | accept it; split packages if you need more |
| **`@Override`** | no inheritance, so nothing to override | embedding promotes, it doesn't override |
| **Spring Cloud / Eureka** | service discovery is an infrastructure concern | DNS, Kubernetes Services, or an ingress |
| **`@Lazy` circular dependency resolution** | circular deps are a design smell | fix the design |

---

## 12.4 Naming and style translations

| Java | Go | Note |
|---|---|---|
| `getUserById` | `GetUserByID` or `ByID` | acronyms are ALL CAPS: `ID`, `URL`, `HTTP`, `API`, `UUID` |
| `UserDTO` | `UserResponse` / `CreateUserRequest` | no `DTO`/`VO`/`BO` suffix convention |
| `IUserService` | `UserService` (interface) | no `I` prefix; name by behaviour |
| `UserServiceImpl` | `PgUserRepository` | name by implementation, not "Impl" |
| `UserManager` | `UserService` | avoid vague nouns |
| `AbstractBaseEntity` | `BaseModel` (embedded) | no "Abstract" prefix |
| `isValid()` | `IsValid()` | no `get` prefix; `Is`/`Has`/`Can` for booleans is idiomatic (`IsActive`, `HasRole`) |
| `MAX_RETRY_COUNT` | `MaxRetryCount` | constants are CamelCase, not SCREAMING |
| `user_name` field | `UserName` or `Name` | Go uses CamelCase, JSON uses snake_case via tags |
| `package com.example.app.user` | `package user` | never `snake_case` or `camelCase` in package names |
| `File userFile` | `f *os.File` | short receiver/local names, length ∝ scope |
| `List<User> users` | `users []User` | |
| `Map<String,User>` | `byEmail map[string]User` | name maps by key |

---

## 12.5 Things that will bite you

Collected from the chapters, in rough order of how often they bite:

1. **Unused imports and unused local variables are compile errors.** Java warns; Go refuses to build.
2. **The opening brace must be on the same line.** Automatic semicolon insertion makes `if x > 0` on one line and `{` on the next a syntax error.
3. **`append` may reallocate — always reassign.** `append(s, x)` alone does nothing useful.
4. **Writing to a nil map panics.** `make()` it first. Reading is fine.
5. **Range loop variables in Go 1.22+ are per-iteration.** Pre-1.22 code needed `v := v` before capturing in a goroutine. You're on 1.27, so this is fixed — but you'll see the old workaround in tutorials.
6. **`gofmt` decides your style.** Don't fight it; configure your editor.
7. **Errors are values, and ignoring one is a bug.** Never `_ = somethingThatCanFail()` without a comment.
8. **`net/http` servers default to no timeouts.** Always set `ReadHeaderTimeout` at minimum.
9. **`srv.Shutdown` needs a fresh context**, not the one that was cancelled.
10. **`stop_grace_period` must exceed your shutdown timeout**, or Docker `SIGKILL`s you.
11. **Compose service hostnames, not `localhost`.** `postgres://user:pw@postgres:5432/...`
12. **`role_level` is inverted in this schema.** Lower is more privileged. `SUPER_USER=1`.
13. **bcrypt only uses the first 72 bytes.** Cap input length.
14. **Never log or return `db.CoreUser` directly** — it contains `Password`.
15. **`SELECT *` + generated structs = brittle refactors.** Switch to explicit columns once the schema settles.
16. **`pgx.ErrNoRows` is not a bug, it's a 404.** Translate it once, at the repository boundary.
17. **`%v` in `fmt.Errorf` breaks the error chain.** Use `%w`.
18. **Pointer vs value receivers: pick one per type.** Mixing breaks interface satisfaction.
19. **Interfaces are satisfied implicitly** — so a typo'd method silently fails to satisfy an interface, and the error only appears where you use it.
20. **`internal/` is enforced by the compiler.** Use it; it's free encapsulation.

---

## 12.6 Quick reference: the request lifecycle

```
Android client
   │  POST /api/v1/users
   ▼
gin engine
   │
   ├─ Recovery          panic → 500
   ├─ RequestID         X-Request-ID in/out, c.Set
   ├─ Logger            times the whole chain
   ├─ CORS              (browsers only)
   │
   ├─ RequireAuth       Bearer → Claims → c.Set(userID, roleLevel)
   ├─ RequireRole(2)    role_level <= 2 ?
   │
   ├─ UserHandler.Create
   │     ├─ bindJSON     → 422 on invalid
   │     └─ svc.Create(ctx, input)
   │           ├─ normalize email
   │           ├─ repo.ByEmail      → pgx → core_user
   │           ├─ hasher.Hash       → bcrypt
   │           └─ repo.Create       → INSERT ... RETURNING *
   │
   └─ respondError (on failure)  → domain.ErrX → 4xx/5xx mapping
```

Only two layers know about HTTP. Only one knows about SQL. That's the whole architecture, and it's why swapping the framework in chapter 06 touches nothing of consequence.

---

Next: [Chapter 13 — Exercises and What's Next](13-roadmap-and-next-steps.md).
