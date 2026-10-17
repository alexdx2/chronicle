resource "aws_lambda_function" "thumb" {
  function_name = "thumbnailer"
  runtime       = "nodejs18.x"
}

resource "aws_lambda_function" "dyn" {
  function_name = "${var.env}-dyn"
}

resource "aws_ecs_service" "api" {
  name            = "orders-ecs"
  cluster         = aws_ecs_cluster.main.id
}

resource "aws_ecs_task_definition" "api" {
  family = "orders-task"
}

resource "aws_s3_bucket" "b" {
  bucket = "my-bucket"
  name   = "not-a-service"
}
