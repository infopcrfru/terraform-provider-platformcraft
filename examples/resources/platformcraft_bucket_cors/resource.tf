resource "platformcraft_bucket" "example" {
  bucket = "my-company-assets"
}

# Правила применяются в заданном порядке: срабатывает первое подходящее.
resource "platformcraft_bucket_cors" "example" {
  bucket = platformcraft_bucket.example.bucket

  rule {
    allowed_origins = ["https://www.example.com"]
    allowed_methods = ["GET", "HEAD"]
    allowed_headers = ["*"]
    max_age_seconds = 3000
  }

  rule {
    allowed_origins = ["https://admin.example.com"]
    allowed_methods = ["GET", "PUT", "POST", "DELETE", "HEAD"]
    allowed_headers = ["*"]
    max_age_seconds = 600
  }
}
