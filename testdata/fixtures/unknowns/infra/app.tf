# Recognized AWS resources that depend on the unknown ones.

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
  tags = {
    widget = acme_widget.core.id
  }
}

resource "aws_dynamodb_table" "state" {
  name         = "state"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  tags = {
    bucket = exoticcloud_bucket.assets.name
  }
}

resource "aws_lambda_function" "worker" {
  function_name = "worker"
  role          = aws_iam_role.worker.arn
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = "worker.zip"

  environment {
    variables = {
      QUEUE_URL   = aws_sqs_queue.jobs.url
      WIDGET_ID   = acme_widget.edge.id
      LOGS_BUCKET = exoticcloud_bucket.logs.name
    }
  }
}
