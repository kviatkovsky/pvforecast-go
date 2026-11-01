# 8. Running the HTTP API on AWS Lambda

- Status: accepted
- Date: 2026-11-01

## Context

A public, always-available endpoint makes the project easy to try, but a
portfolio project should cost close to nothing when idle and must not
require anyone to share credentials. The API is a plain `net/http` server
(ADR 0007).

## Decision

- **Lambda with a Function URL**, not API Gateway: one resource, no extra
  charge, HTTPS and CORS included. Idle cost is zero.
- **AWS Lambda Web Adapter** as a layer, so the unchanged HTTP server runs
  on Lambda. No `aws-lambda-go` dependency, and the same binary serves
  locally, in Docker and on Lambda. Readiness uses `/healthz`.
- **Zip package on `provided.al2023`, arm64**: the binary is uploaded as
  `bootstrap`. No container registry is needed, and arm64 is cheaper per
  GB-second than x86.
- **Terraform owns infrastructure, not code.** The function ignores later
  changes to its package; code ships with `make lambda-deploy` or the
  release-triggered workflow, so a `terraform apply` never rolls back a
  newer deployment.
- **No stored credentials.** Humans use their own credential chain. GitHub
  Actions assumes a role through OIDC that trusts a single environment of a
  single repository and can only update this function.
- **Cost guards**: least-privilege roles, 14-day log retention, optional
  reserved concurrency and an optional monthly budget alert.
- **Opt-in.** The deploy workflow is skipped until the repository variable
  `AWS_DEPLOY_ROLE_ARN` exists. CI checks the module without credentials:
  `terraform fmt`, `validate` and `tflint`.

## Consequences

The function URL is public; abuse is bounded by reserved concurrency (when
the account allows it) and surfaced by the budget alert, not prevented. The
in-memory weather cache lives only as long as a warm execution
environment. The module was validated offline (provider schema, plan,
tflint) but could not be applied from the development environment, which
has no AWS access.
