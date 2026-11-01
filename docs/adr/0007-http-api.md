# 7. HTTP API

- Status: accepted
- Date: 2026-11-01

## Context

The forecast should be usable without installing Go: from other services,
dashboards or a browser. The API will run in a container and on AWS Lambda,
and it calls a free weather API that must not be overloaded.

## Decision

- **Standard library only.** `net/http` with Go 1.22 method patterns,
  `log/slog`, `encoding/json`. No router or framework, consistent with
  [ADR 0002](0002-stdlib-only-core.md).
- **One code path with the CLI.** The JSON system format (`internal/config`)
  and the response (`internal/report`) are shared, and a test checks that
  the API and `pvforecast --format json` give identical output.
- **Two request forms.** `GET` with query parameters for the common
  single-array case (easy to try with curl or a browser), `POST` with the
  `--config` JSON for anything else. Unknown JSON fields are rejected.
- **RFC 9457 problem details** for every error, including the mux's own 404
  and 405, with all invalid fields listed at once and the request ID.
  Upstream failures map to 502/503/504; internal errors are logged and
  reported without detail.
- **Protect the upstream and the instance.** `weather.Cache` keeps forecasts
  for 15 minutes and merges concurrent identical requests into one call. A
  semaphore bounds concurrent forecasts and sheds excess load with 503 and
  `Retry-After` rather than queuing into timeouts. Bodies are capped at
  64 KB.
- **Twelve-factor configuration** through environment variables; JSON logs
  by default.
- **Lambda without an SDK.** The plan is to run the unchanged HTTP server on
  Lambda with the AWS Lambda Web Adapter, so the module keeps zero
  dependencies and the same binary serves locally, in a container and on
  Lambda.

## Consequences

The in-memory cache is per instance; on Lambda it lives as long as the
execution environment. A shared cache (for example ElastiCache) is not
worth its cost at this scale.
