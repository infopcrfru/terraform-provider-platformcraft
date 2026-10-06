terraform {
  required_providers {
    platformcraft = {
      source  = "infopcrfru/platformcraft"
      version = "~> 0.1"
    }
  }
}

# Ключи доступа передаются через переменные окружения
# PLATFORMCRAFT_ACCESS_KEY и PLATFORMCRAFT_SECRET_KEY.
# endpoint и region по умолчанию указывают на PlatformCraft S3.
provider "platformcraft" {}
