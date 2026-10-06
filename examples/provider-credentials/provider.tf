variable "platformcraft_access_key" {
  type      = string
  sensitive = true
}

variable "platformcraft_secret_key" {
  type      = string
  sensitive = true
}

provider "platformcraft" {
  endpoint   = "https://eu-s3.platformcraft.com"
  region     = "eu-central-2"
  access_key = var.platformcraft_access_key
  secret_key = var.platformcraft_secret_key

  # Ограничение частоты запросов от провайдера (по умолчанию 50 в секунду).
  max_requests_per_second = 30
}
