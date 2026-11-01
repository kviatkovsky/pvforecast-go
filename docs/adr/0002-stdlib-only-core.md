# 2. Standard library only in library packages

- Status: accepted
- Date: 2026-11-01

## Context

The library is meant to be embedded in other services (batch jobs, AWS
Lambda functions, HTTP APIs, WebAssembly). Every third-party dependency adds
supply-chain risk, upgrade work and binary size for those users, and the
physics needs nothing beyond `math`, `time` and `net/http`.

## Decision

- Library packages (`solarpos`, `irradiance`, `thermal`, `power`, `weather`,
  `forecast`) import only the Go standard library.
- The module starts with no `require` directives at all. Tests use the
  standard `testing` package and small local helpers (for example,
  tolerance-based float comparison). A test-only dependency such as
  `github.com/google/go-cmp` may be added if it clearly pays for itself; it
  must be justified in the pull request that adds it. Such a dependency is
  never linked into user binaries and, with module graph pruning, is not
  added to users' `go.mod` files.
- The CLI uses the standard `flag` package. JSON is the only configuration
  format, because YAML would need a third-party parser.
- Developer tools (golangci-lint, govulncheck, goreleaser) are invoked as
  pinned external binaries, not added as `tool` directives, so they never
  appear in the module graph of users.
- Logging uses `log/slog` and happens only in the CLI. Library packages report
  through return values and errors.

## Consequences

Users get a module with zero transitive dependencies. Some conveniences (YAML
configuration, assertion libraries) are unavailable and must be written by
hand or avoided.
