# Changelog

## 0.1.0 (5 октября 2026)

Первый публичный релиз.

### Провайдер

- Подключение к S3-хранилищу PlatformCraft: `endpoint`, `region`, `access_key`,
  `secret_key` в блоке `provider` или через переменные окружения
  `PLATFORMCRAFT_*`; значения по умолчанию указывают на
  `https://eu-s3.platformcraft.com` и регион `eu-central-2`.
- Ограничение частоты запросов (`max_requests_per_second` /
  `PLATFORMCRAFT_MAX_RPS`, по умолчанию 50 в секунду) и повтор ответов
  `429 Too Many Requests` — до 6 попыток.
- Журнал HTTP-запросов и ответов: `PLATFORMCRAFT_HTTP_DEBUG=1`.

### Ресурсы

- `platformcraft_bucket` (с `force_destroy`: удаление всех объектов, версий
  и delete-маркеров вместе с бакетом)
- `platformcraft_bucket_versioning`
- `platformcraft_bucket_acl`
- `platformcraft_bucket_policy` (сравнение политик по смыслу, без ложных диффов)
- `platformcraft_bucket_cors` (несколько правил)
- `platformcraft_bucket_object_lock_configuration`
- `platformcraft_object` (из строки или файла; multipart upload для больших файлов)
- `platformcraft_object_copy`
- `platformcraft_object_retention`
- `platformcraft_object_legal_hold`

Все ресурсы поддерживают импорт.

### Data sources

- `platformcraft_buckets`, `platformcraft_bucket`
- `platformcraft_bucket_versioning`, `platformcraft_bucket_acl`,
  `platformcraft_bucket_policy`, `platformcraft_bucket_cors`,
  `platformcraft_bucket_object_lock_configuration`
- `platformcraft_bucket_objects`, `platformcraft_bucket_objects_v1`
- `platformcraft_object` (метаданные, содержимое или скачивание в файл),
  `platformcraft_object_acl`, `platformcraft_object_versions`

### Ephemeral-ресурсы (Terraform 1.10+)

- `platformcraft_object_presigned_get_url`
- `platformcraft_object_presigned_put_url`
