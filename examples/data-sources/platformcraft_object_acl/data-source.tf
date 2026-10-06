data "platformcraft_object_acl" "example" {
  bucket = "my-company-assets"
  key    = "public/robots.txt"
}

output "object_permissions" {
  value = data.platformcraft_object_acl.example.grant[*].permission
}
