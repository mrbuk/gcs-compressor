resource "google_compute_instance" "gcs-compressor-main" {
  name         = "gcs-compressor-main-${random_id.suffix.hex}"
  machine_type = "n2-custom-16-8192"
  zone         = var.zone

  allow_stopping_for_update = true

  boot_disk {
    initialize_params {
      image = "cos-cloud/cos-stable"
    }
  }

  tags = ["ssh"]

  advanced_machine_features {
    threads_per_core = 1
  }

  network_interface {
    subnetwork = "default"
  }

  scheduling {
    automatic_restart   = true
    on_host_maintenance = "MIGRATE"
  }

  service_account {
    email = google_service_account.default.email

    scopes = [
      "https://www.googleapis.com/auth/bigquery",
      "https://www.googleapis.com/auth/cloud-platform",
      "https://www.googleapis.com/auth/devstorage.read_write",
      "https://www.googleapis.com/auth/logging.write",
      "https://www.googleapis.com/auth/monitoring.write",
      "https://www.googleapis.com/auth/pubsub",
      "https://www.googleapis.com/auth/service.management.readonly",
      "https://www.googleapis.com/auth/servicecontrol",
      "https://www.googleapis.com/auth/trace.append"
    ]
  }

  metadata_startup_script = <<EOF
    echo 'DOCKER_OPTS="--registry-mirror=https://mirror.gcr.io"' | tee /etc/default/docker
    sudo systemctl daemon-reload
    sudo systemctl restart docker
EOF

  metadata = {
    enable-oslogin            = "true"
    gce-container-declaration = <<EOF
    spec:
      containers:
      - name: gcs-compressor-main
        image: mrbuk/gcs-compressor:0.1
        args:
        - -compressionLevel=1
        - -sourceBucket=${data.google_storage_bucket.source.name}
        - -destinationBucket=${data.google_storage_bucket.destination.name}
        - -subscription=${google_pubsub_subscription.default.name}
        - -topic=${google_pubsub_topic.default.name}
        - -projectId=${var.project_id}
        stdin: false
        tty: false
        restartPolicy: Always
        # This container declaration format is not public API and may change without notice. Please
        # use gcloud command-line tool or Google Cloud Console to run Containers on Google Compute Engine."
    EOF
    google-logging-enabled    = "true"
    google-monitoring-enabled = "true"
  }
}
