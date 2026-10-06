data "platformcraft_buckets" "all" {}

output "bucket_names" {
  value = data.platformcraft_buckets.all.buckets
}
