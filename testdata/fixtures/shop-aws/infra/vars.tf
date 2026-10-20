variable "db_password" {
  type      = string
  default   = "hunter2"
  sensitive = true
}

resource "aws_ssm_parameter" "db" {
  name  = "/shop/db/password"
  type  = "SecureString"
  value = "hunter2"
}

resource "random_id" "suffix" {
  byte_length = 4
}
