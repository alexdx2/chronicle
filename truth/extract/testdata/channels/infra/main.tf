resource "aws_sqs_queue" "orders" {
  name = "orders-queue-tf"
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
  })
  settings {
    name = "nested"
  }
}

resource "aws_sqs_queue" "dyn" {
  name = "${var.env}-q"
}

resource "aws_sns_topic" "alerts" {
  name = "alerts-topic"
}

resource "kafka_topic" "t" {
  topic_name = "tf-kafka"
}

resource "confluent_kafka_topic" "c" {
  topic_name = "tf-confluent"
}

resource "aws_s3_bucket" "b" {
  name = "not-a-channel"
}
