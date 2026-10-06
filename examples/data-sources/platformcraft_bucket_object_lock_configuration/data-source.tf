data "platformcraft_bucket_object_lock_configuration" "example" {
  bucket = "my-company-archive"
}

output "default_retention" {
  value = "${data.platformcraft_bucket_object_lock_configuration.example.mode}, ${data.platformcraft_bucket_object_lock_configuration.example.days} дн."
}
