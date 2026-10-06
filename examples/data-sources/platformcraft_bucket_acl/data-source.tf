data "platformcraft_bucket_acl" "example" {
  bucket = "my-company-assets"
}

output "bucket_grants" {
  value = [
    for g in data.platformcraft_bucket_acl.example.grant : {
      grantee    = g.grantee_type == "Group" ? g.grantee_uri : g.grantee_id
      permission = g.permission
    }
  ]
}
