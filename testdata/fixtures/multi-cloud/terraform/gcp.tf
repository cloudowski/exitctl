resource "google_pubsub_topic" "orders" {
  name = "orders"
}

resource "google_pubsub_subscription" "orders" {
  name  = "orders-worker"
  topic = google_pubsub_topic.orders.id

  ack_deadline_seconds = 30
}

resource "google_spanner_instance" "main" {
  name         = "shop"
  config       = "regional-europe-west1"
  display_name = "shop"
  num_nodes    = 1
}

resource "google_spanner_database" "db" {
  instance = google_spanner_instance.main.name
  name     = "orders"
}
