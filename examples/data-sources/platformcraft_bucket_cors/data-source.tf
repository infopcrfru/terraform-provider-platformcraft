data "platformcraft_bucket_cors" "example" {
  bucket = "my-company-assets"
}

output "cors_origins" {
  value = flatten(data.platformcraft_bucket_cors.example.rule[*].allowed_origins)
}
