resource "aws_sqs_queue" "billing" {
  name                       = "billing"
  visibility_timeout_seconds = 60
}
