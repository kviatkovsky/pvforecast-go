variable "region" {
  description = "AWS region. eu-central-1 (Frankfurt) is the closest to Central Europe."
  type        = string
  default     = "eu-central-1"
}

variable "name" {
  description = "Name of the Lambda function and prefix of related resources."
  type        = string
  default     = "pvforecast-api"

  validation {
    condition     = can(regex("^[a-zA-Z0-9-]{1,48}$", var.name))
    error_message = "Use 1–48 letters, digits or hyphens."
  }
}

variable "package_path" {
  description = "Zip with the linux/arm64 `bootstrap` binary, built by `make lambda-zip`. Used only when the function is created; later code updates go through `make lambda-deploy` or the deploy workflow."
  type        = string
  default     = "build/bootstrap.zip"
}

variable "memory_mb" {
  description = "Lambda memory. CPU scales with memory; 512 MB computes a 7-day forecast in well under a second."
  type        = number
  default     = 512
}

variable "timeout_seconds" {
  description = "Lambda timeout. The API's own REQUEST_TIMEOUT is set 5 s below it."
  type        = number
  default     = 30

  validation {
    condition     = var.timeout_seconds >= 10 && var.timeout_seconds <= 900
    error_message = "Use 10–900 seconds."
  }
}

variable "reserved_concurrency" {
  description = "Upper bound on concurrent executions, which caps cost if the public URL is abused. null leaves it unreserved (required on new accounts whose total limit is 10)."
  type        = number
  default     = null
}

variable "log_retention_days" {
  description = "CloudWatch log retention."
  type        = number
  default     = 14
}

variable "cors_allow_origins" {
  description = "Origins allowed to call the function URL from a browser."
  type        = list(string)
  default     = ["*"]
}

variable "github_repository" {
  description = "owner/repo allowed to deploy through GitHub Actions OIDC. Empty disables the deploy role."
  type        = string
  default     = "kviatkovsky/pvforecast-go"
}

variable "github_environment" {
  description = "GitHub environment the deploy job must run in; the role trusts only that environment."
  type        = string
  default     = "production"
}

variable "create_github_oidc_provider" {
  description = "Create the GitHub OIDC identity provider. Set to false if the account already has one for token.actions.githubusercontent.com."
  type        = bool
  default     = true
}

variable "budget_email" {
  description = "Email for a monthly cost alert. Empty disables the budget."
  type        = string
  default     = ""
}

variable "budget_limit_usd" {
  description = "Monthly budget in USD; alerts at 80 % actual and 100 % forecast."
  type        = number
  default     = 5
}

variable "tags" {
  description = "Extra tags for every resource."
  type        = map(string)
  default     = {}
}
