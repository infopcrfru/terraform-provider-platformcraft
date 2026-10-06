data "platformcraft_bucket_versioning" "example" {
  bucket = "my-company-assets"
}

output "versioning_status" {
  value = data.platformcraft_bucket_versioning.example.status
}
