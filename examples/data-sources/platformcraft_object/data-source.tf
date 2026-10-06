# Метаданные объекта.
data "platformcraft_object" "manifest" {
  bucket = "my-company-assets"
  key    = "releases/manifest.json"
}

# Метаданные и содержимое небольшого текстового объекта.
data "platformcraft_object" "config" {
  bucket       = "my-company-assets"
  key          = "config/settings.json"
  read_content = true
}

output "settings" {
  value = jsondecode(data.platformcraft_object.config.content)
}
