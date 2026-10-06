# Ссылка на скачивание объекта, действует 1 час. Значение url не попадает
# в plan и state и доступно только в эфемерных контекстах (Terraform 1.10+):
# блоках provider, других ephemeral-ресурсах, write-only атрибутах и locals,
# на которые они ссылаются.
ephemeral "platformcraft_object_presigned_get_url" "example" {
  bucket          = "my-company-assets"
  key             = "reports/2026-q3.pdf"
  expires_seconds = 3600
}
