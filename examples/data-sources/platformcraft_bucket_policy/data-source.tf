data "platformcraft_bucket_policy" "example" {
  bucket = "my-company-assets"
}

# Пустая строка, если политика не задана.
output "bucket_policy" {
  value = data.platformcraft_bucket_policy.example.policy == "" ? null : jsondecode(data.platformcraft_bucket_policy.example.policy)
}
