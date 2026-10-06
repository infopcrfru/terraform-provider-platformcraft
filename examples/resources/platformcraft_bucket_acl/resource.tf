resource "platformcraft_bucket" "example" {
  bucket = "my-company-assets"
}

resource "platformcraft_bucket_acl" "example" {
  bucket = platformcraft_bucket.example.bucket
  acl    = "private"
}
