resource "platformcraft_bucket" "example" {
  bucket = "my-company-assets"
}

# Публичное чтение объектов с префиксом public/.
resource "platformcraft_bucket_policy" "example" {
  bucket = platformcraft_bucket.example.bucket
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "PublicReadOnly"
      Effect    = "Allow"
      Principal = "*"
      Action    = ["s3:GetObject"]
      Resource  = ["arn:aws:s3:::${platformcraft_bucket.example.bucket}/public/*"]
    }]
  })
}
