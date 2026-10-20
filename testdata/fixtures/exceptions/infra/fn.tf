resource "aws_iam_role" "lambda" {
  name = "order-processor"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
}

resource "aws_lambda_function" "order_processor" {
  function_name = "order-processor"
  role          = aws_iam_role.lambda.arn
  runtime       = "nodejs20.x"
  handler       = "index.handler"
  filename      = "build/order-processor.zip"

  vpc_config {
    subnet_ids         = [aws_subnet.a.id, aws_subnet.b.id]
    security_group_ids = [aws_security_group.app.id]
  }

  environment {
    variables = {
      TABLE_NAME = aws_dynamodb_table.orders.name
      QUEUE_URL  = aws_sqs_queue.events.url
    }
  }
}

resource "aws_lambda_event_source_mapping" "orders_stream" {
  event_source_arn  = aws_dynamodb_table.orders.stream_arn
  function_name     = aws_lambda_function.order_processor.arn
  starting_position = "LATEST"
}

resource "aws_lambda_event_source_mapping" "events_queue" {
  event_source_arn = aws_sqs_queue.events.arn
  function_name    = aws_lambda_function.order_processor.arn
  batch_size       = 10
}
