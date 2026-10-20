resource "rabbitmq_exchange" "ev" {
  name  = "tf.events"
  vhost = "/"
  settings {
    type = "topic"
    name = "nested-ignored"
  }
}

resource "rabbitmq_queue" "q" {
  name  = "tf.queue"
  vhost = "/"
}

resource "rabbitmq_queue" "dyn" {
  name = "${var.env}-q"
}

resource "rabbitmq_binding" "b" {
  source      = rabbitmq_exchange.ev.name
  destination = rabbitmq_queue.q.name
}

# resource "rabbitmq_queue" "old" {
#   name = "tf.old"
# }
