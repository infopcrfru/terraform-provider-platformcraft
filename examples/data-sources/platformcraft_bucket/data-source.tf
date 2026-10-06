data "platformcraft_bucket" "example" {
  bucket = "my-company-assets"
}

output "bucket_usage" {
  value = {
    objects    = data.platformcraft_bucket.example.object_count
    size_bytes = data.platformcraft_bucket.example.total_size_bytes
    versioning = data.platformcraft_bucket.example.versioning_status
  }
}
