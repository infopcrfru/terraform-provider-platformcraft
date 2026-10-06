resource "platformcraft_bucket" "example" {
  bucket = "my-company-assets"

  # Версионируемый бакет удаляется только вместе со всеми версиями объектов.
  force_destroy = true
}

resource "platformcraft_bucket_versioning" "example" {
  bucket = platformcraft_bucket.example.bucket
  status = "Enabled"
}
