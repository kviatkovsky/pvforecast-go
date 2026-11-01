output "function_url" {
  description = "Public HTTPS endpoint of the API."
  value       = aws_lambda_function_url.api.function_url
}

output "function_name" {
  description = "Lambda function name, for `make lambda-deploy` and the AWS_LAMBDA_FUNCTION repository variable."
  value       = aws_lambda_function.api.function_name
}

output "deploy_role_arn" {
  description = "Role for the GitHub deploy workflow (AWS_DEPLOY_ROLE_ARN repository variable)."
  value       = local.github_enabled ? aws_iam_role.deploy[0].arn : null
}
