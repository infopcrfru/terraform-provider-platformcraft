resource "platformcraft_bucket" "example" {
  bucket = "my-company-archive"
}

# Object Lock работает только при включённом версионировании.
resource "platformcraft_bucket_versioning" "example" {
  bucket = platformcraft_bucket.example.bucket
  status = "Enabled"
}

# Правило хранения по умолчанию для новых объектов бакета.
resource "platformcraft_bucket_object_lock_configuration" "example" {
  bucket = platformcraft_bucket.example.bucket
  mode   = "GOVERNANCE"
  days   = 30

  depends_on = [platformcraft_bucket_versioning.example]
}
