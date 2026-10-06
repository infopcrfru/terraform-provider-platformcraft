resource "platformcraft_object_copy" "example" {
  source_bucket = "my-company-assets"
  source_key    = "releases/app-1.0.0.zip"

  bucket = "my-company-archive"
  key    = "releases/2026/app-1.0.0.zip"
}
