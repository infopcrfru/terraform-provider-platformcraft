resource "platformcraft_bucket" "example" {
  bucket        = "my-company-assets"
  force_destroy = true
}

# Небольшой текстовый объект из строки.
resource "platformcraft_object" "robots" {
  bucket  = platformcraft_bucket.example.bucket
  key     = "public/robots.txt"
  content = "User-agent: *\nDisallow:\n"
  acl     = "public-read"
}

# Объект из локального файла. Большие файлы автоматически загружаются
# по частям (multipart upload).
resource "platformcraft_object" "release" {
  bucket      = platformcraft_bucket.example.bucket
  key         = "releases/app-1.0.0.zip"
  source_path = "${path.module}/build/app-1.0.0.zip"
}
