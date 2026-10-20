resource "aws_db_instance" "main" {
  identifier = "main"
  engine     = "postgres"
  username   = "admin"
  password   = "tf-Pa55word-seeded-1"
}

resource "aws_secretsmanager_secret_version" "api" {
  secret_id     = "api"
  secret_string = "AKIAIOSFODNN7EXAMPLE"
}
