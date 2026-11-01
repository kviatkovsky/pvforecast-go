# Contributing to pvforecast-go

Thanks for your interest. This document covers how to set up a development
environment and what a pull request needs in order to be merged.

## Development setup

You need Go 1.26 or newer. The `toolchain` directive in `go.mod` makes the Go
command download the exact toolchain used in CI.

```bash
git clone https://github.com/kviatkovsky/pvforecast-go
cd pvforecast-go
make check
```

`make check` runs what CI runs: `go mod tidy -diff`, `go vet`, `golangci-lint`
and the tests with the race detector. Run `make help` for all targets.

Install [golangci-lint](https://golangci-lint.run/welcome/install/) v2.15.0 or
newer as a standalone binary.

## Ground rules

- **Library packages use only the standard library.** See
  [ADR 0002](docs/adr/0002-stdlib-only-core.md).
- **Physics changes cite their source.** Every model implements a published
  method. Doc comments name the paper and the equation number; do not add
  empirical corrections that are not in the literature.
- **Physics changes come with reference values.** Add or update the pvlib
  fixtures in `internal/testdata` and state the tolerance you test against.
- **Units are SI.** Angles are degrees at the API boundary and radians
  internally; times are UTC `time.Time`.
- **Tests are table-driven** and cover edge cases (sun below the horizon,
  zenith near 90°, polar day and night, NaN inputs).
- **No logging in library packages.** Return errors; wrap them with `%w`.

## Commits and pull requests

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
(`feat(irradiance): add Perez transposition`). The release changelog is
generated from them.

Keep pull requests focused on one change. Describe what changed, why, and how
it was validated, including numerical results against pvlib for physics
changes.

## Recording decisions

Significant design or modelling decisions are recorded as ADRs in
[`docs/adr`](docs/adr). Add one when your change makes such a decision.

## Releasing

Releases are cut by pushing a `v*` tag on `main`. The `Release` workflow runs
the tests, then GoReleaser builds the binaries, archives, SBOMs and the
signed checksum file, pushes the multi-arch API image to
`ghcr.io/kviatkovsky/pvforecast-api` (signed, with an SBOM attestation),
and publishes the GitHub release with a changelog grouped by Conventional
Commit type. Check the configuration locally with
`goreleaser check` and `goreleaser build --snapshot --clean`.
