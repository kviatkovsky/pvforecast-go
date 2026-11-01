# The unchanged HTTP server runs on Lambda behind the AWS Lambda Web Adapter
# extension, which translates invocations into HTTP requests on
# localhost:8080. See docs/adr/0008-aws-lambda.md.

locals {
  # Published by AWS in every commercial region (aws/aws-lambda-web-adapter).
  web_adapter_layer = "arn:aws:lambda:${var.region}:753240598075:layer:LambdaAdapterLayerArm64:30"
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/aws/lambda/${var.name}"
  retention_in_days = var.log_retention_days
}

data "aws_iam_policy_document" "assume_lambda" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "api" {
  name               = "${var.name}-exec"
  assume_role_policy = data.aws_iam_policy_document.assume_lambda.json
}

# Least privilege: write to this function's log group only, instead of the
# account-wide AWSLambdaBasicExecutionRole.
data "aws_iam_policy_document" "logs" {
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.api.arn}:*"]
  }
}

resource "aws_iam_role_policy" "logs" {
  name   = "logs"
  role   = aws_iam_role.api.id
  policy = data.aws_iam_policy_document.logs.json
}

resource "aws_lambda_function" "api" {
  function_name = var.name
  description   = "pvforecast HTTP API"
  role          = aws_iam_role.api.arn

  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]
  layers        = [local.web_adapter_layer]

  filename         = var.package_path
  source_code_hash = filebase64sha256(var.package_path)

  memory_size                    = var.memory_mb
  timeout                        = var.timeout_seconds
  reserved_concurrent_executions = var.reserved_concurrency

  environment {
    variables = {
      AWS_LWA_PORT                 = "8080"
      AWS_LWA_READINESS_CHECK_PATH = "/healthz"
      PORT                         = "8080"
      LOG_FORMAT                   = "json"
      REQUEST_TIMEOUT              = "${var.timeout_seconds - 5}s"
      CACHE_TTL                    = "15m"
    }
  }

  depends_on = [aws_cloudwatch_log_group.api, aws_iam_role_policy.logs]

  lifecycle {
    # Terraform owns the infrastructure; code is deployed with
    # `make lambda-deploy` or the deploy workflow, and must not be rolled
    # back by the next `terraform apply`.
    ignore_changes = [filename, source_code_hash]
  }
}

resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.api.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = var.cors_allow_origins
    allow_methods = ["GET", "POST"]
    allow_headers = ["content-type"]
    max_age       = 3600
  }
}

# A public (NONE) function URL needs both statements: InvokeFunctionUrl,
# and InvokeFunction restricted to calls that arrive through the URL.
resource "aws_lambda_permission" "url" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.api.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

resource "aws_lambda_permission" "url_invoke" {
  statement_id             = "FunctionURLAllowInvokeAction"
  action                   = "lambda:InvokeFunction"
  function_name            = aws_lambda_function.api.function_name
  principal                = "*"
  invoked_via_function_url = true
}
