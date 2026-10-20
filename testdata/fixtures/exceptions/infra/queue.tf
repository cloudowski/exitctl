resource "aws_sqs_queue" "events" {
  name                       = "events"
  visibility_timeout_seconds = 60
}
