# Список объектов бакета через ListObjects (V1) — вместе с владельцем объекта.
data "platformcraft_bucket_objects_v1" "example" {
  bucket = "my-company-assets"
}

output "object_owners" {
  value = { for o in data.platformcraft_bucket_objects_v1.example.objects : o.key => o.owner_id }
}
