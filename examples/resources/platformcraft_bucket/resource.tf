resource "platformcraft_bucket" "example" {
  # Имя бакета уникально во всей системе PlatformCraft, а не только в аккаунте.
  bucket = "my-company-assets"

  # При terraform destroy удалить все объекты, их версии и delete-маркеры.
  force_destroy = true
}
