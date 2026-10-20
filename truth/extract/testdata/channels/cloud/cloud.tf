resource "kafka_topic" "orders" {
  name               = "tf-orders"
  replication_factor = 3
  config = {
    "name" = "nested-not-top-level"
  }
}

# resource "kafka_topic" "old" {
#   name = "tf-old"
# }

resource "google_pubsub_topic" "sig" {
  name    = "tf-signals"
  project = var.p
}

resource "google_pubsub_subscription" "sub" {
  name  = "tf-sub"
  topic = google_pubsub_topic.sig.name
}

resource "google_pubsub_topic" "dyn" {
  name = "${var.env}-dyn"
}

resource "google_pubsub_topic" "viavar" {
  name = var.topic_name
}
