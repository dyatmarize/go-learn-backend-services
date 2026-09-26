# Chapter 01 — Go for Java Developers

> **Goal:** get past the syntax shock. By the end of this chapter you should be able to read Go code without translating it back to Java in your head, and you should know which Java reflexes to deliberately suppress.

There is no `mvn` here and no container. Go is a small language with a small standard library and a strong opinion: **explicit beats implicit, and simple beats clever.** Most Spring Boot concepts you rely on are things Go expects you to write yourself.

---

## 1.1 Packages and the module

A Go program is a set of **packages**. A package is just a directory of `.go` files. The module (declared in `go.mod`) gives those packages importable paths.

```go
// internal/config/config.go
package config

import (
	"fmt"
	"os"
)

func Load() (*Config, error) {
	// ...
}
```

```go
// cmd/api/main.go
package main

import (
	"log"

	"learn101/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	_ = cfg
}
```

| Java | Go |
|---|---|
| `package com.example.app.config;` | `package config` — flat, one package per directory |
| `import com.example.app.config.Config;` | `import "learn101/internal/config"` — you import the **package**, then use `config.Config` |
| Directory name and package name are unrelated | Directory name and package name **must match** (by convention, always) |
| `public class Config` | `type Config struct` + capitalized fields |
| `private String secret` | lowercase field: `secret string` |

`package main` plus `func main()` is the entry point. There is no `public class Application { public static void main }` ceremony — just those two things.

**Rule to internalize now:** an identifier is **exported** (visible to other packages) if it starts with a capital letter. `User` is public, `user` is package-private. That's the entire visibility system. No `public`, `private`, or `protected` keywords exist.

---

## 1.2 Variables, and the zero value

There is no `null` for primitives, and no such thing as an uninitialized variable. Every type has a **zero value**.

```go
var (
	i     int        // 0
	f     float64    // 0
	s     string     // ""  (empty, never null)
	b     bool       // false
	p     *User      // nil
	slice []string   // nil  (len 0, works with append)
	m     map[string]int // nil (READING is fine, WRITING panics)
)

// := declares and infers
count := 42
name := "dyatmarize"
```

The Go equivalent of `int` (primitive) simply cannot be null — you must use `*int` for "maybe absent", and then you get a nil check.

```java
// Java
Integer maybeCount = null;          // boxed, nullable
String  maybeName  = null;
```

```go
// Go
var maybeCount *int                    // nil
maybeName := (*string)(nil)            // nil
```

**Reflex to suppress:** don't reach for pointers just because Java made you. Use `*T` when absence is meaningful, or when you need to mutate. Otherwise use the value.

### `:=` vs `var` vs `const`

```go
x := 10              // short declaration, inferred type — most common inside functions
var y int = 10       // explicit
var z int            // zero value
const MaxRetries = 3 // compile-time constant

// Multiple assignment
a, b := 1, 2
a, b = b, a          // swap, no temp variable
```

### No implicit conversions

Java widens `int` → `long` silently. Go does not.

```go
var i int32 = 5
var j int64 = int64(i)   // the conversion is mandatory
```

This bites when you compare `int64` DB columns against `int` literals. It's a feature: it forces you to be explicit about the width you want.

---

## 1.3 Slices and maps

### Slices — the `ArrayList` you'll actually use

```go
var nums []int              // nil slice, len 0
nums = append(nums, 1, 2)   // append returns a NEW slice — must reassign
nums = append(nums, 3)

users := []string{"ana", "budi"}
for i, name := range users {
	fmt.Println(i, name)   // index, value
}

for _, name := range users { // ignore the index with _
	fmt.Println(name)
}

sub := users[0:1]           // "ana" — a view, not a copy
```

| Java | Go |
|---|---|
| `List<String> l = new ArrayList<>();` | `var l []string` |
| `l.add("x")` | `l = append(l, "x")` |
| `l.get(0)` | `l[0]` |
| `l.size()` | `len(l)` |
| `for (String s : l)` | `for _, s := range l` |
| `l.contains("x")` | `slices.Contains(l, "x")` (Go 1.21+, `slices` package) |
| `Collections.sort(l)` | `slices.Sort(l)` |

**The two traps:**

1. `append` may reallocate. If you forget to reassign (`append(l, x)` on its own line), the compiler flags it as an unused value — but if you pass a slice to a function expecting it to grow, it won't. Slices are a **(pointer, length, capacity)** triple passed by value.
2. `s[0:1]` shares the underlying array with `s`. Mutating one mutates the other.

### Maps

```go
ages := map[string]int{
	"dyatmarize": 30,
}

ages["admin"] = 25          // writing to a nil map panics — always make() first
delete(ages, "admin")

age, ok := ages["missing"]  // comma-ok: ok is false, age is zero value
if !ok {
	// not present
}

for k, v := range ages {    // iteration order is RANDOM — never rely on it
	fmt.Println(k, v)
}
```

The comma-ok idiom is Go's answer to `map.containsKey()`. You will write it constantly — including for type assertions and channel receives.

---

## 1.4 Structs, methods, and the death of inheritance

A struct is a bag of fields. A method is a function with a **receiver**.

```go
type User struct {
	ID        int64
	UUID      string
	Name      string
	Email     string
	RoleLevel int64
	DeletedAt *time.Time   // nil == not deleted (soft delete)
}

// Value receiver: gets a COPY. Use for read-only methods.
func (u User) IsActive() bool {
	return u.DeletedAt == nil
}

// Pointer receiver: gets the address, can mutate. Use when mutating,
// or when the struct is large enough that copying is wasteful.
func (u *User) SoftDelete(now time.Time) {
	u.DeletedAt = &now
}
```

```java
// Java
public class User {
	private Long id;
	private String email;
	public boolean isActive() { return deletedAt == null; }
	public void softDelete(Instant now) { this.deletedAt = now; }
}
```

**There is no `extends`.** Go gives you two tools instead:

### Embedding (composition, with promotion)

```go
type BaseModel struct {
	ID        int64
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

type User struct {
	BaseModel            // embedded — no field name
	Name      string
	Email     string
	RoleID    int64
}
```

Because `BaseModel` is embedded without a name, `User` **promotes** its fields:

```go
u := User{}
u.ID = 1                    // promoted, same as u.BaseModel.ID
u.CreatedAt = time.Now()
```

This is *not* inheritance. There is no method overriding, no `super`, no polymorphic substitution. It's field/method promotion and nothing more.

### Interfaces (implicit, and small)

This is the part that surprises Java developers most: **a type never declares that it implements an interface.** It just does, if it has the methods.

```go
type UserReader interface {
	ByID(ctx context.Context, id int64) (*User, error)
	ByEmail(ctx context.Context, email string) (User, error)
}

// this type satisfies UserReader purely by having those two methods.
// No "implements UserReader" anywhere.
type PgUserRepo struct {
	pool *pgxpool.Pool
}

func (r *PgUserRepo) ByID(ctx context.Context, id int64) (*User, error) { /* ... */ }
func (r *PgUserRepo) ByEmail(ctx context.Context, email string) (User, error) { /* ... */ }
```

Consequences worth internalizing:

- Interfaces are usually **defined where they are consumed**, not where the implementation lives. The consumer says "I need something that can fetch a user."
- Idiomatic Go interfaces are **tiny** — one to three methods. `io.Reader` is one method and it changed the ecosystem.
- Because satisfaction is structural, you can write a fake for testing without touching the real type.

```go
type fakeUserReader struct {
	users map[int64]*User
}

func (f *fakeUserReader) ByID(ctx context.Context, id int64) (*User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (f *fakeUserReader) ByEmail(ctx context.Context, email string) (User, error) {
	return User{}, ErrNotFound
}
```

That's the Go equivalent of a Mockito mock, hand-written, with no framework. Chapter 09 builds on this.

**Rule of thumb:** accept interfaces, return structs. A function should take an interface when it only needs a behavior, and return a concrete type so callers keep all the methods.

---

## 1.5 Errors are values

This is the single biggest adjustment. Go has no exceptions. Functions that can fail return `error` as their **last** return value.

```go
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()
	// ...
	return cfg, nil
}
```

```java
// Java
public Config loadConfig(String path) throws IOException {
	try (var f = new FileInputStream(path)) { ... }
}
```

Key points:

- `error` is an interface with one method, `Error() string`. `nil` means success. There is no try/catch/finally.
- `%w` in `fmt.Errorf` **wraps** the error, preserving the chain (like Java's `cause`).
- `defer` is Go's `finally` — it runs when the function returns, in LIFO order.

Callers inspect errors with `errors.Is` (sentinel comparison) and `errors.As` (type extraction):

```go
// Sentinel errors — package-level values callers can compare against
var (
	ErrNotFound      = errors.New("not found")
	ErrEmailTaken    = errors.New("email already registered")
	ErrInvalidCreds  = errors.New("invalid credentials")
)
```

```go
_, err := repo.ByEmail(ctx, email)
switch {
case errors.Is(err, ErrNotFound):
	// 404
case err != nil:
	// 500, unexpected
default:
	// happy path
}
```

```go
// errors.As extracts a custom error TYPE
var appErr *AppError
if errors.As(err, &appErr) {
	fmt.Println(appErr.Code, appErr.Status)
}
```

**Reflexes to suppress:**

- Don't build a decorator-cake of exception hierarchies. Use a small set of sentinel errors plus one custom error type for HTTP mapping (chapter 08).
- **Do not ignore errors.** `_ = doThing()` is a code smell. `godotenv.Load(".env")` being ignored in the current `main.go` is exactly the bug this rule prevents.
- Don't panic. `panic` is for programmer error (nil map write, index out of range), never for "the user sent a bad email". Panics in an HTTP handler kill the goroutine serving one request; recovery middleware (chapter 08) exists to convert that into a 500.

---

## 1.6 Pointers, without the fear

You already use references in Java — everything non-primitive is one. Go makes the distinction visible.

```go
u := User{Name: "dyatmarize"}   // u is a value
p := &u                          // p is *User, a pointer to u
p.Name = "dyat"                  // mutates u through the pointer
fmt.Println(u.Name)              // "dyat"

q := new(User)                   // *User pointing at a zeroed User
fmt.Println(q == nil)            // false — new() allocates
```

When to use a pointer:

| Situation | Use |
|---|---|
| Method mutates the struct | pointer receiver `func (u *User)` |
| Large struct, avoid copying | pointer receiver |
| Field that is genuinely optional (`deleted_at`) | `*time.Time` |
| Small immutable data | value |
| A `sync.Mutex` field | pointer (never copy a mutex) |

Go has **no garbage-collection escape hatch** like Java's `finalize`, and **no `null` dereference protection** — dereferencing a nil pointer panics. `pgx` returns `pgx.ErrNoRows` rather than a nil struct, which is why chapter 05 checks for it.

One crucial difference: **Go has no `this`.** The receiver is just a parameter you name (conventionally one letter: `u`, `r`, `s`). And a method on `*T` is only in `*T`'s method set, so if a type needs to satisfy an interface, decide value vs pointer receivers **once** and be consistent — mixing them is a common compile error.

---

## 1.7 No annotations, no DI container

This is the chapter in miniature:

```java
// Java
@Service
@RequiredArgsConstructor
public class UserService {
	@Autowired private final UserRepository repo;
	@Autowired private final PasswordEncoder encoder;

	@Transactional
	public UserDto create(CreateUserRequest req) { ... }
}
```

```go
// Go — internal/service/user.go
type UserService struct {
	repo   UserStore   // an interface, defined nearby
	hasher PasswordHasher
}

// The "constructor". Wire everything by hand.
func NewUserService(repo UserStore, hasher PasswordHasher) *UserService {
	return &UserService{repo: repo, hasher: hasher}
}

func (s *UserService) Create(ctx context.Context, in CreateUserInput) (*User, error) {
	// no @Transactional: transactions are explicit (chapter 05)
	return nil, nil
}
```

There is no container scanning for `@Component`. Wiring happens in one place — usually a `internal/app` or `internal/server` package — and it looks like this:

```go
func Build(cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) *gin.Engine {
	repo := repository.NewUserRepository(pool)   // wraps sqlc's db.New(pool)
	hasher := auth.NewBcryptHasher()
	svc := service.NewUserService(repo, hasher)
	userHandler := handler.NewUserHandler(svc, log)

	r := gin.New()
	// ... register routes
	return r
}
```

Read that and you know the entire object graph. No reflection, no startup magic, no "why is this bean not found" at 2am. **This explicitness is the main thing people end up preferring about Go.**

The cost: you write the wiring yourself. For a service this size, that's ~20 lines.

---

## 1.8 `context.Context` — the thing Java didn't make you pass

Every handler, service and repository method takes `ctx context.Context` as its **first parameter**. It carries:

- **Cancellation** — the client hung up, so stop working.
- **Deadlines** — this request has 3 seconds.
- **Request-scoped values** — request IDs, authenticated identity.

```go
func (r *UserRepository) ByUUID(ctx context.Context, uuid string) (*db.CoreUser, error) {
	// ctx flows into the query: if the client disconnects, the query is cancelled
	return r.q.GetUserByUUID(ctx, uuid)
}
```

Rules:

- Take `ctx` as the first arg, name it `ctx`, pass it down. Never store it in a struct.
- Never pass `nil` — use `context.Background()` at the very top of `main`.
- Propagate it to the DB. `pgx` honours cancellation, so a disconnected Android client stops costing you a query.
- Values in context are for request-scoped metadata only, never for dependencies.

You'll set up `signal.NotifyContext` in chapter 11 so `Ctrl-C` cancels in-flight work gracefully.

---

## 1.9 `defer`

`defer` schedules a call to run when the surrounding function returns. It's `finally`, but declared where the resource is acquired.

```go
func run() error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()          // runs on ANY return path

	rows, err := pool.Query(ctx, "select 1")
	if err != nil {
		return err                 // pool.Close() still runs
	}
	defer rows.Close()           // LIFO: rows.Close() then pool.Close()

	return nil
}
```

Gotcha: `defer` arguments are evaluated **immediately**, at the point of the `defer` statement — not when it runs. This is why `defer f.Close()` captures the right file even inside a loop.

---

## 1.10 Goroutines and channels (a primer)

Go's concurrency is not a thread pool you configure. It's `go func()` and channels.

```go
// A goroutine: a function running concurrently. Cheap — thousands are normal.
go func() {
	log.Println("runs concurrently")
}()

// Channels: typed, blocking, synchronizing pipes.
results := make(chan string)

go func() {
	results <- "done"     // send (blocks until someone receives)
}()

msg := <-results          // receive (blocks until something is sent)
fmt.Println(msg)
```

```go
// The standard fan-out pattern with a WaitGroup
var wg sync.WaitGroup
for _, url := range urls {
	wg.Add(1)
	go func() {
		defer wg.Done()
		fetch(url)
	}()
}
wg.Wait()
```

For this project you need almost none of this — an HTTP server is already concurrent, one goroutine per request. But you must understand **why** `main` blocking on `srv.ListenAndServe()` is not a bug, and why shared state between handlers needs a mutex or a channel. (It won't, if you keep state in the database.)

Comparison to Java: no `ExecutorService` to size, no virtual-thread configuration, no `CompletableFuture` chains for basic concurrency. Goroutines start at ~2KB of stack and grow.

---

## 1.11 Generics (a primer)

Go has had generics since 1.18. They're used in library code far more than application code, but you'll meet them in `slices` and `maps`.

```go
func Map[T, U any](in []T, f func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

names := Map(users, func(u User) string { return u.Name })
```

```java
// Java
public static <T, U> List<U> map(List<T> in, Function<T, U> f) { ... }
```

You will use `slices` and `maps` helpers long before you write your own generic function. Don't over-apply them: Go idiom prefers a concrete function over a clever generic one.

---

## 1.12 The Java → Go cheat table

| Java | Go |
|---|---|
| `String` | `string` |
| `Integer` / `int` | `int` / `int64` / `int32` — pick a width, no implicit widening |
| `Boolean` | `bool` (zero value `false`) |
| `List<T>` | `[]T` |
| `Map<K,V>` | `map[K]V` |
| `Set<T>` | `map[T]struct{}` (no built-in set) |
| `Optional<T>` | `*T` or a comma-ok bool |
| `null` | `nil` (pointers, slices, maps, channels, interfaces) |
| `interface` | `interface` (implicit satisfaction) |
| `class` + `extends` | `struct` + embedding (**no inheritance**) |
| `abstract class` | interface + struct |
| `final` | no keyword; `const` for values, unexported fields are not settable outside the package |
| `try/catch/finally` | `error` returns + `defer` |
| `throw new FooException()` | `return fmt.Errorf("...: %w", err)` |
| `instanceof` + cast | type assertion `v, ok := x.(T)` or `errors.As` |
| `toString()` | `String() string` method |
| `equals()`/`hashCode()` | `==` (structs compare field-by-field; **maps and slices are not comparable**) |
| `new Foo()` | `&Foo{}` or `new(Foo)` |
| `Foo.class` | `reflect.TypeOf(Foo{})` (rarely needed) |
| `synchronized` | `sync.Mutex` / channels |
| `@Annotation` | nothing — plain code |
| `static` fields | package-level variables |
| `static` methods | package-level functions |
| `void` | no return value, or `error` only |
| constructor | `func NewXxx(...) *Xxx` (a **convention**, not a language feature) |
| `finalize()` | `runtime.SetFinalizer` (avoid) |
| serialization | `encoding/json` struct tags: `json:"email"` |

---

## 1.13 Exercises

1. Write `internal/firststeps/main.go` (package `main`) that declares a `User` struct with `ID int64`, `Name string`, `DeletedAt *time.Time`, gives it an `IsActive() bool` method and a `SoftDelete(time.Time)` method, then prints `IsActive()` before and after.
2. Add a `[]User` slice, `append` two users, and range over it printing index and name.
3. Write a function `FindByID(users []User, id int64) (*User, error)` returning a sentinel `ErrNotFound`, and call it with `errors.Is`.
4. Deliberately trigger a nil-map write and read the panic. Then `make()` the map and watch it work. This is the fastest way to internalize which types need `make`.
5. Convert this Java to Go:
   ```java
   abstract class Shape { abstract double area(); }
   class Circle extends Shape { double r; double area() { return Math.PI*r*r; } }
   ```
   Hint: you can't. Define a `Shape` interface with `Area() float64` and let `Circle` satisfy it implicitly.

## 1.14 Done when

- [ ] You can explain why `append(l, x)` alone on a line is a bug.
- [ ] You can explain the difference between a value receiver and a pointer receiver, and which one satisfies an interface.
- [ ] You can write an interface, an implementation that satisfies it implicitly, and a hand-written fake — without looking anything up.
- [ ] You know what `%w` does and why `errors.Is` exists.
- [ ] You know why `ctx context.Context` is the first parameter of nearly every function you'll write.

Next: [Chapter 02 — Modules, Layout and Tooling](02-modules-layout-and-tooling.md).
