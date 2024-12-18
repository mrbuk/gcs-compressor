resource "random_id" "suffix" {
  byte_length = 4
}

// enable required services
resource "google_project_service" "default" {
  project = var.project_id

  for_each = toset([
    "compute.googleapis.com",
    "storage.googleapis.com",
    "pubsub.googleapis.com"
  ])

  service = each.key

  timeouts {
    create = "30m"
    update = "40m"
  }

  disable_on_destroy = false
}


// Default Service Account used in GCE

resource "google_service_account" "default" {
  account_id   = "gcs-compressor-${random_id.suffix.hex}"
  display_name = "Service Account"
}

// Source Bucket

data "google_storage_bucket" "source" {
  name = var.source_bucket_name
}

resource "google_storage_bucket_iam_member" "source_member" {
  bucket = data.google_storage_bucket.source.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.default.email}"
}

// Destination Bucket

data "google_storage_bucket" "destination" {
  name = var.destination_bucket_name
}

resource "google_storage_bucket_iam_member" "destination_member" {
  bucket = data.google_storage_bucket.destination.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.default.email}"
}

// PubSub topic used to receive notifications

resource "google_pubsub_topic" "default" {
  name = "export-data-objects-${random_id.suffix.hex}"
}

resource "google_pubsub_topic_iam_member" "gcs_serivce_account" {
  topic  = google_pubsub_topic.default.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${data.google_storage_project_service_account.gcs_account.email_address}"
}

resource "google_pubsub_subscription_iam_member" "default_service_account" {
  subscription = google_pubsub_subscription.default.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${google_service_account.default.email}"
}

resource "google_pubsub_subscription" "default" {
  name  = "export-data-objects-sub-${random_id.suffix.hex}"
  topic = google_pubsub_topic.default.id

  ack_deadline_seconds = 30

  enable_exactly_once_delivery = true
}

// Storage notifications

resource "google_storage_notification" "default" {
  bucket         = data.google_storage_bucket.source.name
  payload_format = "JSON_API_V1"
  topic          = google_pubsub_topic.default.id
  depends_on     = [google_pubsub_topic_iam_member.gcs_serivce_account]
}

data "google_storage_project_service_account" "gcs_account" {
}

