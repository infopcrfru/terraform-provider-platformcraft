# Список объектов бакета (ListObjectsV2, все страницы).
data "platformcraft_bucket_objects" "example" {
  bucket = "my-company-assets"
}

output "object_keys" {
  value = data.platformcraft_bucket_objects.example.objects[*].key
}
