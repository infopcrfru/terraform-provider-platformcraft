# ==============================================================================
# Пример для terraform-provider-platformcraft: все ресурсы, все data sources и
# ephemeral resource presigned GET URL. Сценарий проверки — ../../TESTING.md.
# ==============================================================================

terraform {
  required_providers {
    platformcraft = {
      source  = "infopcrfru/platformcraft"
      version = "~> 0.1"
    }
  }
}

provider "platformcraft" {
  # endpoint и region по умолчанию указывают на PlatformCraft.
  # Ключи — в переменных окружения PLATFORMCRAFT_ACCESS_KEY и
  # PLATFORMCRAFT_SECRET_KEY (README.md, раздел 1.2).
}

# Имя бакета уникально во всей системе PlatformCraft, а не только в аккаунте.
# Передайте своё: -var="bucket_name=..." или TF_VAR_bucket_name.
variable "bucket_name" {
  type        = string
  description = "Имя тестового бакета, уникальное во всей системе PlatformCraft."
}

# Срок retention для platformcraft_object_retention.demo. Должен быть в будущем.
variable "retain_until" {
  type    = string
  default = "2027-12-31T00:00:00Z"
}

# ==============================================================================
# Бакет и все его bucket-level настройки
# ==============================================================================

resource "platformcraft_bucket" "demo" {
  bucket = var.bucket_name

  # При destroy удалить оставшиеся версии объектов: бакет версионируется,
  # и без очистки удаление непустого бакета завершится BucketNotEmpty.
  force_destroy = true
}

resource "platformcraft_bucket_versioning" "demo" {
  bucket = platformcraft_bucket.demo.bucket
  status = "Enabled"
}

resource "platformcraft_bucket_acl" "demo" {
  bucket = platformcraft_bucket.demo.bucket
  acl    = "private"
}

resource "platformcraft_bucket_policy" "demo" {
  bucket = platformcraft_bucket.demo.bucket
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "PublicReadOnly"
      Effect    = "Allow"
      Principal = "*"
      Action    = ["s3:GetObject"]
      Resource  = ["arn:aws:s3:::${var.bucket_name}/public/*"]
    }]
  })
}

# Несколько CORS-правил на одном ресурсе — применяются в заданном порядке.
resource "platformcraft_bucket_cors" "demo" {
  bucket = platformcraft_bucket.demo.bucket

  rule {
    allowed_origins = ["*"]
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

# Object Lock требует включённого версионирования — отсюда явная зависимость.
resource "platformcraft_bucket_object_lock_configuration" "demo" {
  bucket = platformcraft_bucket.demo.bucket
  mode   = "GOVERNANCE"
  days   = 1

  depends_on = [platformcraft_bucket_versioning.demo]
}

# ==============================================================================
# Объекты
# ==============================================================================

resource "platformcraft_object" "demo" {
  bucket  = platformcraft_bucket.demo.bucket
  key     = "hello.txt"
  content = "Hello from Terraform!"
  acl     = "private"

  # На бакете включён Object Lock GOVERNANCE по умолчанию: объект получает
  # retention при создании, и без bypass destroy не сможет его удалить.
  bypass_governance_retention = true
  refresh_content             = false

  depends_on = [platformcraft_bucket_object_lock_configuration.demo]
}

resource "platformcraft_object_copy" "demo_copy" {
  source_bucket = platformcraft_bucket.demo.bucket
  source_key    = platformcraft_object.demo.key
  bucket        = platformcraft_bucket.demo.bucket
  key           = "hello-copy.txt"

  # Копия тоже получает retention по умолчанию бакета.
  bypass_governance_retention = true
}

resource "platformcraft_object_retention" "demo" {
  bucket       = platformcraft_bucket.demo.bucket
  key          = platformcraft_object.demo.key
  mode         = "GOVERNANCE"
  retain_until = var.retain_until

  depends_on = [platformcraft_bucket_object_lock_configuration.demo]
}

resource "platformcraft_object_legal_hold" "demo" {
  bucket  = platformcraft_bucket.demo.bucket
  key     = platformcraft_object.demo.key
  enabled = true

  depends_on = [platformcraft_bucket_object_lock_configuration.demo]
}

# ==============================================================================
# Data sources читают то, что создали ресурсы выше.
#
# depends_on нужен там, где data source ссылается только на имя бакета: оно
# задаётся в конфигурации, а не вычисляется ресурсом, поэтому без depends_on
# Terraform может прочитать настройку до того, как ресурс её применит.
# ==============================================================================

data "platformcraft_buckets" "all" {}

data "platformcraft_bucket" "demo" {
  bucket = platformcraft_bucket.demo.bucket
  depends_on = [
    platformcraft_bucket_versioning.demo,
    platformcraft_object.demo,
    platformcraft_object_copy.demo_copy,
  ]
}

data "platformcraft_bucket_versioning" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_bucket_versioning.demo]
}

data "platformcraft_bucket_acl" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_bucket_acl.demo]
}

data "platformcraft_bucket_policy" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_bucket_policy.demo]
}

data "platformcraft_bucket_cors" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_bucket_cors.demo]
}

data "platformcraft_bucket_object_lock_configuration" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_bucket_object_lock_configuration.demo]
}

data "platformcraft_bucket_objects" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_object.demo, platformcraft_object_copy.demo_copy]
}

data "platformcraft_bucket_objects_v1" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_object.demo, platformcraft_object_copy.demo_copy]
}

data "platformcraft_object" "demo" {
  bucket       = platformcraft_bucket.demo.bucket
  key          = platformcraft_object.demo.key
  read_content = true
}

data "platformcraft_object_acl" "demo" {
  bucket = platformcraft_bucket.demo.bucket
  key    = platformcraft_object.demo.key
}

data "platformcraft_object_versions" "demo" {
  bucket     = platformcraft_bucket.demo.bucket
  depends_on = [platformcraft_object.demo, platformcraft_object_copy.demo_copy]
}

# ==============================================================================
# Ephemeral resource: presigned URL (Terraform 1.10+). Значение не попадает в
# plan и state, а вывести его через output корневого модуля Terraform не
# позволяет, поэтому здесь проверяется только успешное открытие ресурса.
# Проверку самого URL выполняет acceptance-тест
# TestAccObjectPresignedGetUrlEphemeral_basic.
# ==============================================================================
ephemeral "platformcraft_object_presigned_get_url" "demo" {
  bucket          = platformcraft_bucket.demo.bucket
  key             = platformcraft_object.demo.key
  expires_seconds = 600
}

# ==============================================================================
# Выводы для ручной проверки после apply
# ==============================================================================

output "bucket_object_count" {
  value = data.platformcraft_bucket.demo.object_count
}

output "all_bucket_names" {
  value = data.platformcraft_buckets.all.buckets
}

output "demo_object_etag" {
  value = platformcraft_object.demo.etag
}
