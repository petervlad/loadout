# Go standards for loadout

The rules this codebase follows. When in doubt, the sources win:
[Effective Go](https://go.dev/doc/effective_go) (idioms only, it predates generics and modules),
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments),
[Google Go Style Guide](https://google.github.io/styleguide/go/),
[Uber Go Style Guide](https://github.com/uber-go/guide),
[Organizing a Go module](https://go.dev/doc/modules/layout).

## Toolchain

- Go 1.27 (`go 1.27` in `go.mod`). `CGO_ENABLED=0` for every build.
- Use modern stdlib: `log/slog`, `errors.Join`, `errors.AsType`, `os.Root`, `t.Context()`, `ServeMux` method patterns.

## Design ("OOP" in Go)

- **Composition, not inheritance.** Build types from fields. Never embed in exported structs.
- **Consumers define interfaces.** The package that *uses* a dependency declares the small interface it needs.
- **Accept interfaces, return concrete types.** `func New(...) *Installer`.
- **No interface without a second implementation or a test seam.**
- **Compile-time checks:** `var _ install.Renderer = (*Adapter)(nil)`.
- **Constructors + plain dependency injection.** Everything is wired by hand in `cmd/loadout`. No globals, no `init()`, no DI framework.
- **Encapsulation:** unexported fields, `internal/` packages, copy slices/maps crossing a boundary.
- **Naming:** no `Get` prefix, don't repeat the package name (`lock.Store`, not `lock.LockStore`), receivers are one or two letters, never `self`.
- **Enums** that are written to files are typed string constants with a `Valid()` method, so files stay readable. In-memory-only enums start at `iota + 1`.
- **Options:** an options struct when callers set most fields, functional options when most callers set none.

## Layout

- `cmd/loadout`: `main` calls `run` and exits in one place.
- `internal/<noun>`: one package per concept, named after what it provides.
- No `pkg/`, `util`, `common`, `helper` or `model` packages.

## Errors

- Wrap with context and `%w` last: `fmt.Errorf("install %s: %w", id, err)`.
- Matchable + fixed message: sentinel `var ErrX = errors.New(...)`. Matchable + dynamic: a typed error.
- Match with `errors.Is` / `errors.AsType`, never on strings.
- Handle an error once: log it or return it, not both.
- Lowercase messages, no trailing punctuation, never discard with `_`.
- No `panic`, `log.Fatal` or `os.Exit` outside `main`.

## Context, logging, concurrency, HTTP

- `ctx context.Context` is the first parameter, never stored in a struct.
- One `*slog.Logger` built in `main` and injected. Keyed attributes.
- Synchronous APIs; every goroutine has an owner and a stop.
- HTTP servers set all four timeouts, cap bodies with `http.MaxBytesReader`, shut down gracefully.

## Files on disk

- Write through `os.Root` so a bad name can never escape the target directory.
- Write atomically: temp file in the same directory, then rename.
- Never overwrite a file loadout did not write, or one the user changed, without `--force`.

## Tests

- Table-driven with `t.Run`; messages read `Func(in) = got, want want`.
- No assertion libraries; `go-cmp` for structs.
- Fakes over mocks; `t.TempDir()`, `testdata/`, golden files refreshed with `-update`.
- Always `go test -race ./...`.

## Tooling

- `make check` runs format, lint (golangci-lint v2, config in `.golangci.yml`), tests with the race detector, and govulncheck.
