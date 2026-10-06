# Все версии объектов и delete-маркеры версионируемого бакета.
data "platformcraft_object_versions" "example" {
  bucket = "my-company-assets"
}

output "current_versions" {
  value = {
    for v in data.platformcraft_object_versions.example.version : v.key => v.version_id
    if v.is_latest
  }
}
