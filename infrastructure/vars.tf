variable "project_id" {
  type = string
}

variable "source_bucket_name" {
  type = string
}

variable "destination_bucket_name" {
  type = string
}

variable "zone" {
  type    = string
  default = "europe-west3-b"
}