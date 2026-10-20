resource "aws_cognito_user_pool" "main" {
  name = "shop-users"

  password_policy {
    minimum_length = 12
  }
}

resource "aws_cognito_user_pool_client" "web" {
  name         = "shop-web"
  user_pool_id = aws_cognito_user_pool.main.id
}
