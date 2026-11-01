# Deploy the HTTP API to AWS Lambda

This Terraform module runs `pvforecast-api` on AWS Lambda behind a public
Function URL. It is "bring your own account": nothing here contains or needs
stored credentials, and the GitHub deploy workflow stays disabled until you
opt in.

```text
client ──HTTPS──▶ Lambda Function URL ──▶ Lambda Web Adapter (layer) ──HTTP :8080──▶ pvforecast-api
```

The same binary that runs locally and in the container runs unchanged on
Lambda: the [AWS Lambda Web Adapter](https://github.com/aws/aws-lambda-web-adapter)
extension turns each invocation into an HTTP request on localhost. See
[ADR 0008](../../docs/adr/0008-aws-lambda.md).

## What gets created

| Resource | Notes |
|---|---|
| Lambda function | `provided.al2023`, **arm64** (cheaper than x86), 512 MB, 30 s, Web Adapter layer |
| Function URL | public (`NONE` auth), CORS for GET/POST |
| CloudWatch log group | 14-day retention |
| IAM execution role | may write to its own log group only |
| GitHub OIDC provider + deploy role | optional; trusts only the `production` environment of this repository; may only update this function's code |
| AWS Budget | optional; email at 80 % actual / 100 % forecast of the monthly limit |

## Cost

With portfolio-level traffic this stays inside the AWS **always-free** tier:
Lambda includes 1 million requests and 400,000 GB-seconds per month, Function
URLs have no extra charge, and CloudWatch Logs include 5 GB of ingestion.
Expect **≈ 0 PLN/month**. Check current prices on the
[Lambda pricing page](https://aws.amazon.com/lambda/pricing/).

The URL is public, so set `budget_email`, and once your account's
concurrency limit allows it, `reserved_concurrency` (for example 5) to cap
how much abuse can cost.

## Deploy

Prerequisites: Terraform ≥ 1.9, Go, `zip`, the AWS CLI, and credentials for
your account in the standard chain (for example an SSO profile).

```bash
aws sso login --profile my-account && export AWS_PROFILE=my-account
```

```bash
make lambda-zip
```

```bash
cd deploy/aws && cp terraform.tfvars.example terraform.tfvars && terraform init && terraform apply
```

```bash
curl "$(terraform output -raw function_url)healthz"
```

State is local by default (`terraform.tfstate`, ignored by git). For shared
use, add an S3 backend.

## Update the code

Terraform owns the infrastructure; it does not roll code back. Ship new
code either from your machine:

```bash
make lambda-deploy LAMBDA_FUNCTION="$(terraform -chdir=deploy/aws output -raw function_name)"
```

or automatically on every GitHub release:

1. Create the `production` environment in the repository settings (you can
   require a reviewer there).
2. Set repository variables from the Terraform outputs:

```bash
gh variable set AWS_DEPLOY_ROLE_ARN --body "$(terraform -chdir=deploy/aws output -raw deploy_role_arn)"
```

```bash
gh variable set AWS_LAMBDA_FUNCTION --body "$(terraform -chdir=deploy/aws output -raw function_name)"
```

```bash
gh variable set AWS_REGION --body eu-central-1
```

The [deploy workflow](../../.github/workflows/deploy-aws.yml) then builds
the package, assumes the role through OIDC (no AWS keys in GitHub), updates
the function and smoke-tests `/healthz`. Until `AWS_DEPLOY_ROLE_ARN` is set,
the workflow is skipped.

## Remove everything

```bash
cd deploy/aws && terraform destroy
```

This deletes every resource above, including the log group and its logs.
