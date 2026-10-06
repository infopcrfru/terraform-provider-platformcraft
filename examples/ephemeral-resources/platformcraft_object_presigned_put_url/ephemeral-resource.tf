# Ссылка на загрузку объекта методом PUT, действует 15 минут. Значение url
# не попадает в plan и state и доступно только в эфемерных контекстах
# (Terraform 1.10+).
ephemeral "platformcraft_object_presigned_put_url" "example" {
  bucket          = "my-company-assets"
  key             = "uploads/incoming.bin"
  expires_seconds = 900
}
