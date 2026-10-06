# Тестирование terraform-provider-platformcraft

1. [Подготовка](#1-подготовка)
2. [Ручной сценарий: apply, идемпотентность, изменение, destroy](#2-ручной-сценарий)
3. [Дрифт](#3-дрифт)
4. [Импорт](#4-импорт)
5. [Автотесты](#5-автотесты)
6. [Диагностика](#6-диагностика)

Команды даны для bash и PowerShell. Команды `aws s3api` требуют AWS CLI,
настроенный на ключи PlatformCraft (`aws configure` или переменные
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`).

---

## 1. Подготовка

Проверять можно опубликованную версию из Terraform Registry или локальную
сборку. Для локальной сборки соберите провайдер и настройте `dev_overrides`
(README.md, раздел 5.1). `terraform init` с `dev_overrides` не нужен, а пока
провайдер не опубликован в Registry, он завершится ошибкой — пропустите его.

Сценарий выполняется на конфигурации `examples/complete/main.tf`:

```powershell
$env:PLATFORMCRAFT_ACCESS_KEY = "<...>"
$env:PLATFORMCRAFT_SECRET_KEY = "<...>"
$env:BUCKET = "my-unique-test-bucket-123"
$env:S3 = "https://eu-s3.platformcraft.com"
cd examples\complete
terraform init   # только для версии из Registry
```

```bash
export PLATFORMCRAFT_ACCESS_KEY=<...>
export PLATFORMCRAFT_SECRET_KEY=<...>
export BUCKET=my-unique-test-bucket-123
export S3=https://eu-s3.platformcraft.com
cd examples/complete
terraform init   # только для версии из Registry
```

В PowerShell везде используйте `$env:BUCKET`, а не `$BUCKET`: это разные
переменные, и незаданная `$BUCKET` даст пустое имя бакета (провайдер отклонит
его на этапе plan). Многострочные команды с `\` в PowerShell не работают,
поэтому для PowerShell каждая команда приведена в одну строку.

---

## 2. Ручной сценарий

```bash
terraform plan  -var="bucket_name=$BUCKET"                 # 10 ресурсов к созданию
terraform apply -auto-approve -var="bucket_name=$BUCKET"
terraform plan  -var="bucket_name=$BUCKET"                 # No changes
```

```powershell
terraform plan  -var="bucket_name=$env:BUCKET"
terraform apply -auto-approve -var="bucket_name=$env:BUCKET"
terraform plan  -var="bucket_name=$env:BUCKET"
```

Ожидаемый результат:

- `apply` создаёт ресурсы в порядке зависимостей: бакет → versioning →
  Object Lock → объект, retention, legal hold, копия; ACL, policy и CORS зависят
  только от бакета. Data sources и ephemeral resource читаются после ресурсов,
  от которых зависят.
- Повторный `plan` показывает `No changes`. Дифф на ресурсе, который не
  менялся, — ошибка в `Read()` этого ресурса.

**Изменение ресурса.** В `examples/complete/main.tf` поменяйте `max_age_seconds`
первого правила `platformcraft_bucket_cors.demo` с 3000 на 1800:

```bash
terraform plan  -var="bucket_name=$BUCKET"                 # 1 to change
terraform apply -auto-approve -var="bucket_name=$BUCKET"
```

```powershell
terraform plan  -var="bucket_name=$env:BUCKET"
terraform apply -auto-approve -var="bucket_name=$env:BUCKET"
```

Для этого шага не подходит `platformcraft_bucket_versioning`: на бакете с
Object Lock версионирование нельзя перевести в `Suspended`.

**Удаление.**

```bash
terraform destroy -auto-approve -var="bucket_name=$BUCKET"
```

```powershell
terraform destroy -auto-approve -var="bucket_name=$env:BUCKET"
```

Ожидаемый результат: всё удаляется без ошибок, с двумя предупреждениями —
Object Lock и версионирование на бакете с Object Lock не отключаются
(README.md, раздел 4).

Атрибуты, от которых зависит удаление (`bypass_governance_retention`,
`force_destroy`), берутся из state. Если вы меняете их в конфигурации
существующей инфраструктуры, сначала выполните `apply`, затем `destroy`.

---

## 3. Дрифт

Настройка меняется в обход Terraform, затем `terraform plan` должен показать
разницу. Проверки 3.1–3.3 выполняются на стенде из раздела 2 после `apply`.

### 3.1. CORS

```bash
cat > cors-drift.json <<'JSON'
{"CORSRules":[{"AllowedOrigins":["*"],"AllowedMethods":["GET"],"AllowedHeaders":["*"],"MaxAgeSeconds":42}]}
JSON
aws s3api put-bucket-cors --bucket "$BUCKET" --cors-configuration file://cors-drift.json --endpoint-url "$S3"
terraform plan -var="bucket_name=$BUCKET"
```

```powershell
'{"CORSRules":[{"AllowedOrigins":["*"],"AllowedMethods":["GET"],"AllowedHeaders":["*"],"MaxAgeSeconds":42}]}' | Set-Content -Path cors-drift.json -Encoding ascii
aws s3api put-bucket-cors --bucket $env:BUCKET --cors-configuration file://cors-drift.json --endpoint-url $env:S3
terraform plan -var="bucket_name=$env:BUCKET"
```

Ожидаемо: `platformcraft_bucket_cors.demo` будет изменён — два правила из
конфигурации вместо одного фактического.

### 3.2. Bucket policy

Сначала та же политика в другом формате — диффа быть не должно (политики
сравниваются по смыслу). Затем другая политика — дифф должен появиться.

```bash
cat > policy-same.json <<JSON
{"Version":"2012-10-17","Statement":[{"Sid":"PublicReadOnly","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::$BUCKET/public/*"}]}
JSON
aws s3api put-bucket-policy --bucket "$BUCKET" --policy file://policy-same.json --endpoint-url "$S3"
terraform plan -var="bucket_name=$BUCKET"          # No changes

cat > policy-diff.json <<JSON
{"Version":"2012-10-17","Statement":[{"Sid":"Different","Effect":"Allow","Principal":"*","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::$BUCKET"]}]}
JSON
aws s3api put-bucket-policy --bucket "$BUCKET" --policy file://policy-diff.json --endpoint-url "$S3"
terraform plan -var="bucket_name=$BUCKET"          # platformcraft_bucket_policy.demo будет изменён
```

```powershell
$same = '{"Version":"2012-10-17","Statement":[{"Sid":"PublicReadOnly","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::' + $env:BUCKET + '/public/*"}]}'
Set-Content -Path policy-same.json -Value $same -Encoding ascii
aws s3api put-bucket-policy --bucket $env:BUCKET --policy file://policy-same.json --endpoint-url $env:S3
terraform plan -var="bucket_name=$env:BUCKET"

$diff = '{"Version":"2012-10-17","Statement":[{"Sid":"Different","Effect":"Allow","Principal":"*","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::' + $env:BUCKET + '"]}]}'
Set-Content -Path policy-diff.json -Value $diff -Encoding ascii
aws s3api put-bucket-policy --bucket $env:BUCKET --policy file://policy-diff.json --endpoint-url $env:S3
terraform plan -var="bucket_name=$env:BUCKET"
```

### 3.3. Legal hold объекта

```bash
aws s3api put-object-legal-hold --bucket "$BUCKET" --key hello.txt --legal-hold Status=OFF --endpoint-url "$S3"
terraform plan -var="bucket_name=$BUCKET"
```

```powershell
aws s3api put-object-legal-hold --bucket $env:BUCKET --key hello.txt --legal-hold Status=OFF --endpoint-url $env:S3
terraform plan -var="bucket_name=$env:BUCKET"
```

Ожидаемо: `platformcraft_object_legal_hold.demo` — `enabled: false -> true`.

### 3.4. Versioning

На бакете из `examples/complete/main.tf` не проверяется: с Object Lock версионирование
нельзя приостановить даже через `aws s3api` (`InvalidBucketState`). Нужен
отдельный бакет без Object Lock, например такая конфигурация в отдельной папке:

```hcl
terraform {
  required_providers {
    platformcraft = { source = "infopcrfru/platformcraft" }
  }
}

resource "platformcraft_bucket" "v" {
  bucket = "versioning-drift-test-<ваш-суффикс>"
}

resource "platformcraft_bucket_versioning" "v" {
  bucket = platformcraft_bucket.v.bucket
  status = "Enabled"
}
```

```bash
terraform apply -auto-approve
aws s3api put-bucket-versioning --bucket versioning-drift-test-<ваш-суффикс> --versioning-configuration Status=Suspended --endpoint-url "$S3"
terraform plan        # status: "Suspended" -> "Enabled"
terraform destroy -auto-approve
```

```powershell
terraform apply -auto-approve
aws s3api put-bucket-versioning --bucket versioning-drift-test-<ваш-суффикс> --versioning-configuration Status=Suspended --endpoint-url $env:S3
terraform plan
terraform destroy -auto-approve
```

### 3.5. Что не подходит для проверки дрифта

`platformcraft_bucket_acl` не сравнивает `acl` с фактическими грантами
(README.md, раздел 4). Фактические гранты показывают data sources
`platformcraft_bucket_acl` и `platformcraft_object_acl`.

---

## 4. Импорт

### 4.1. ID = имя бакета

Для `platformcraft_bucket`, `_bucket_versioning`, `_bucket_acl`,
`_bucket_policy`, `_bucket_cors`, `_bucket_object_lock_configuration`.

```bash
aws s3api create-bucket --bucket import-test-<ваш-суффикс> --endpoint-url "$S3"
```

```powershell
aws s3api create-bucket --bucket import-test-<ваш-суффикс> --endpoint-url $env:S3
```

Добавьте в `examples/complete/main.tf`:

```hcl
resource "platformcraft_bucket" "imported" {
  bucket = "import-test-<ваш-суффикс>"
}
```

```bash
terraform import -var="bucket_name=$BUCKET" platformcraft_bucket.imported import-test-<ваш-суффикс>
terraform plan   -var="bucket_name=$BUCKET"        # No changes
```

```powershell
terraform import -var="bucket_name=$env:BUCKET" platformcraft_bucket.imported import-test-<ваш-суффикс>
terraform plan   -var="bucket_name=$env:BUCKET"
```

Если `plan` предлагает пересоздать все ресурсы с `bucket = ... -> null`,
проверьте, что передано правильное имя бакета (в PowerShell — `$env:BUCKET`).

Уборка: удалите блок `platformcraft_bucket.imported` из конфигурации, затем

```bash
terraform state rm platformcraft_bucket.imported
aws s3api delete-bucket --bucket import-test-<ваш-суффикс> --endpoint-url "$S3"
```

```powershell
terraform state rm platformcraft_bucket.imported
aws s3api delete-bucket --bucket import-test-<ваш-суффикс> --endpoint-url $env:S3
```

### 4.2. ID = `<bucket>,<key>`

Для `platformcraft_object`, `_object_copy`, `_object_retention`,
`_object_legal_hold`. Разделитель — запятая: ключ может содержать `/`.

```bash
printf 'imported content' > imported.txt
aws s3api put-object --bucket "$BUCKET" --key imported.txt --body imported.txt --endpoint-url "$S3"
```

```powershell
Set-Content -Path imported.txt -Value "imported content" -NoNewline -Encoding ascii
aws s3api put-object --bucket $env:BUCKET --key imported.txt --body imported.txt --endpoint-url $env:S3
```

Добавьте в `examples/complete/main.tf` (подставьте имя своего бакета):

```hcl
resource "platformcraft_object" "imported" {
  bucket  = "my-unique-test-bucket-123"
  key     = "imported.txt"
  content = "imported content"
}
```

```bash
terraform import -var="bucket_name=$BUCKET" platformcraft_object.imported "$BUCKET,imported.txt"
terraform plan   -var="bucket_name=$BUCKET"
```

```powershell
terraform import -var="bucket_name=$env:BUCKET" platformcraft_object.imported "$env:BUCKET,imported.txt"
terraform plan   -var="bucket_name=$env:BUCKET"
```

После импорта `content` в state пустой — API его не возвращает, поэтому первый
`plan` покажет `+ content = "imported content"` (обновление на месте). Это
ожидаемо: после `apply` объект будет перезаписан тем же содержимым, и
следующий `plan` будет пустым.

Уборка:

```bash
terraform state rm platformcraft_object.imported
aws s3api delete-object --bucket "$BUCKET" --key imported.txt --endpoint-url "$S3"
```

```powershell
terraform state rm platformcraft_object.imported
aws s3api delete-object --bucket $env:BUCKET --key imported.txt --endpoint-url $env:S3
```

---

## 5. Автотесты

### 5.1. Модульные тесты

Без сети и без ключей; запускаются в CI на каждый push и pull request
(`.github/workflows/ci.yml`).

| Файл | Что проверяет |
|---|---|
| `internal/provider/json_utils_test.go` | Сравнение bucket policy по смыслу, разбор ID импорта |
| `internal/pcs3/consistency_test.go` | Логика повторов запросов, распознавание пустой политики |
| `internal/pcs3/objects_test.go` | URL-кодирование `x-amz-copy-source`, presigned URL без `x-id` |
| `internal/pcs3/ratelimit_test.go` | Ограничение частоты запросов и повтор ответов 429 |

```bash
go mod download
gofmt -l .        # пусто; иначе gofmt -w .
go vet ./...
go build ./...
go test ./...
```

Без `TF_ACC=1` acceptance-тесты пропускаются (`SKIP`) — это штатное поведение
`terraform-plugin-testing`.

### 5.2. Acceptance-тесты

Работают с настоящим PlatformCraft: создают и удаляют бакеты и объекты. Каждый
тест создаёт свои бакеты с уникальными именами вида
`tf-acc-<имя-теста>-<метка-времени>` и после завершения удаляет их.

**Требования:**

- Terraform CLI 1.10+ в `PATH` (или путь в `TF_ACC_TERRAFORM_PATH`). Если
  Terraform не найден, `terraform-plugin-testing` скачает последнюю версию
  с releases.hashicorp.com.
- Ключи PlatformCraft с правами на создание и удаление бакетов.
- Сеть до `eu-s3.platformcraft.com` (или `PLATFORMCRAFT_ENDPOINT`).

**Запуск всех тестов:**

```bash
export TF_ACC=1
export PLATFORMCRAFT_ACCESS_KEY=<...>
export PLATFORMCRAFT_SECRET_KEY=<...>
go test ./internal/provider/ -run '^TestAcc' -v -count=1 -timeout 60m
```

```powershell
$env:TF_ACC = "1"
$env:PLATFORMCRAFT_ACCESS_KEY = "<...>"
$env:PLATFORMCRAFT_SECRET_KEY = "<...>"
go test ./internal/provider/ -run '^TestAcc' -v -count=1 -timeout 60m
```

Если русский текст в выводе PowerShell отображается «кракозябрами», перед
запуском выполните `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8`.

`-count=1` отключает кэш результатов `go test`: без него повторный запуск без
изменений в коде покажет `(cached)` и ничего не отправит в API.

**Запуск части тестов** — через регулярное выражение в `-run`:

```bash
go test ./internal/provider/ -v -count=1 -run '^TestAccBucketPolicyResource_basic$'   # один тест
go test ./internal/provider/ -v -count=1 -run '^TestAccObject'                          # все про объекты
go test ./internal/provider/ -v -count=1 -run 'DataSource'                              # все data sources
go test ./internal/provider/ -v -count=1 -run 'Ephemeral'                               # presigned URL
```

В PowerShell команды те же; регулярное выражение — в одинарных кавычках, как выше.

**Состав:**

| Тест | Что проверяет |
|---|---|
| `TestAccBucketResource_basic` | создание, проверка через API, импорт, отсутствие бакета после destroy |
| `TestAccBucketResource_disappears` | бакет удалён вне Terraform → plan предлагает создать заново |
| `TestAccBucketResource_forceDestroy` | `force_destroy` удаляет бакет со старыми версиями и объектами, созданными вне Terraform |
| `TestAccBucketVersioningResource_basic` | Enabled → Suspended, импорт |
| `TestAccBucketAclResource_basic` | private → public-read → private, импорт |
| `TestAccBucketPolicyResource_basic` | создание, изменение, импорт, отсутствие вечного диффа, дрифт (политику заменили и удалили) |
| `TestAccBucketCorsResource_multipleRules` | два правила, изменение правила, импорт, дрифт (CORS удалили) |
| `TestAccBucketCorsResource_defaultMaxAge` | правило без `max_age_seconds` получает 3000, повторный plan пустой |
| `TestAccBucketObjectLockConfigurationResource_basic` | GOVERNANCE 1 день → 2 дня, импорт |
| `TestAccObjectResource_basic` | создание, изменение содержимого, импорт `<bucket>,<key>` |
| `TestAccObjectResource_acl` | ACL объекта public-read → private |
| `TestAccObjectResource_sourcePath` | загрузка из файла, ключ с пробелами и кириллицей |
| `TestAccObjectResource_refreshContentDrift` | `refresh_content` видит изменение содержимого вне Terraform |
| `TestAccObjectResource_disappears` | объект удалён вне Terraform → plan предлагает создать заново |
| `TestAccObjectCopyResource_basic` | копия в другой бакет под ключом с кириллицей и пробелами, смена источника, импорт |
| `TestAccObjectRetentionResource_basic` | retention GOVERNANCE, продление срока, импорт, снятие при удалении |
| `TestAccObjectLegalHoldResource_basic` | включение, выключение, импорт, дрифт (legal hold включили вне Terraform) |
| `TestAccBucketsDataSource_basic` | созданный бакет есть в списке |
| `TestAccBucketDataSource_basic` | статус версионирования, количество (2) и размер (7 байт) объектов |
| `TestAccBucketDataSource_notFound` | понятная ошибка для несуществующего бакета |
| `TestAccBucketVersioningDataSource_basic` | статус Enabled |
| `TestAccBucketAclDataSource_basic` | владелец бакета |
| `TestAccBucketPolicyDataSource_basic` | политика содержит заданные Sid и Action |
| `TestAccBucketCorsDataSource_basic` | оба CORS-правила |
| `TestAccBucketObjectLockConfigurationDataSource_basic` | режим и срок Object Lock |
| `TestAccBucketObjectsDataSource_basic` | оба объекта с размерами (list-objects-v2) |
| `TestAccBucketObjectsV1DataSource_basic` | то же через list-objects v1 |
| `TestAccObjectDataSource_basic` | метаданные, `read_content`, `download_path` |
| `TestAccObjectDataSource_notFound` | понятная ошибка для несуществующего ключа |
| `TestAccObjectAclDataSource_basic` | владелец объекта |
| `TestAccObjectVersionsDataSource_basic` | после перезаписи объекта видны 2 версии, последняя помечена |
| `TestAccObjectPresignedGetUrlEphemeral_basic` | скачивание по presigned GET URL возвращает содержимое объекта |
| `TestAccObjectPresignedPutUrlEphemeral_basic` | загрузка по presigned PUT URL создаёт объект |

Каждый шаг теста, кроме шагов импорта, завершается повторным `plan`, и тест
падает, если план не пустой. Поэтому любой «вечный дифф» ловится автоматически.

Ephemeral-тесты передают URL в служебный провайдер `echo` из
`terraform-plugin-testing` (ephemeral-значения в state не попадают) и делают по
URL настоящий HTTP-запрос. На Terraform ниже 1.10 они пропускаются.

**Лимит запросов.** Провайдер и вспомогательный клиент тестов работают в
одном процессе и вместе держат не больше 50 запросов в секунду; ответы 429
повторяются. Не запускайте одновременно второй прогон тестов или другие
интенсивные программы под теми же ключами (раздел 6.1).

**Время и ресурсы.** Полный прогон занимает порядка 15–30 минут (зависит от
задержек API) и создаёт около 35 бакетов, по одному-два на тест. Тесты
выполняются последовательно.

**Если тест упал.** В выводе будет шаг и ошибка API. Бакеты упавшего теста
удаляются автоматически после теста (снимаются legal hold, удаляются все
версии с обходом GOVERNANCE); если и это не удалось, в выводе будет строка
`очистка: не удалось удалить бакет ..., удалите его вручную`. Все тестовые
бакеты начинаются с `tf-acc-`:

```bash
aws s3api list-buckets --endpoint-url "$S3" --query "Buckets[?starts_with(Name, 'tf-acc-')].Name"
```

```powershell
aws s3api list-buckets --endpoint-url $env:S3 --query "Buckets[?starts_with(Name, 'tf-acc-')].Name"
```

Если тест упал с ошибкой API, перезапустите его отдельно через `-run`, чтобы
отличить разовый сбой от регрессии. Для разбора включите журнал HTTP-запросов
(раздел 6.2).

### 5.3. Acceptance-тесты в GitHub Actions

`.github/workflows/acceptance.yml` запускается вручную: Actions →
Acceptance Tests → Run workflow (можно указать своё выражение для `-run`).
Нужны секреты репозитория `PLATFORMCRAFT_ACCESS_KEY` и
`PLATFORMCRAFT_SECRET_KEY` (Settings → Secrets and variables → Actions).
Terraform устанавливается шагом `hashicorp/setup-terraform`. Чтобы тесты
запускались по расписанию, добавьте в `on:` триггер `schedule`.

### 5.4. Проверка кода тестов без PlatformCraft

Тесты можно прогнать против любого S3-совместимого сервера, указав
`PLATFORMCRAFT_ENDPOINT` (например, локальный http://127.0.0.1:7070). Это
проверяет сам код тестов и провайдера, но не поведение PlatformCraft: у
другого сервера свои ограничения (например, ACL или Object Lock на бакете,
созданном без флага Object Lock, могут не поддерживаться).

---

## 6. Диагностика

### 6.1. Лимиты API

Лимиты PlatformCraft по умолчанию (расширяются по запросу):

| Лимит | Значение | Что будет при превышении |
|---|---|---|
| Частота запросов | 70 запросов/с на клиента | ответ `429 Too Many Requests` |
| Скорость загрузки | 240 Мбит/с на клиента | передача замедляется |

Лимит общий для всех программ, которые работают с API под одними ключами:
Terraform, acceptance-тесты, aws-cli, собственные скрипты. Их запросы
складываются.

- Провайдер сам ограничивает себя 50 запросами в секунду
  (`max_requests_per_second` / `PLATFORMCRAFT_MAX_RPS`) и повторяет ответы 429
  (до 6 попыток с нарастающей паузой), поэтому Terraform и тесты в лимит не
  упираются, если рядом не работает что-то ещё.
- Если параллельно идёт другая интенсивная работа с API, уменьшите
  `PLATFORMCRAFT_MAX_RPS`. Если лимит для ваших ключей расширили — увеличьте,
  оставляя запас примерно 20%.

Большие объекты: 1 ГБ при 240 Мбит/с загружается не быстрее чем за ~35 секунд,
10 ГБ — за ~6 минут. Для `terraform apply` с большими `source_path` это
нормальная длительность, а не зависание.

### 6.2. Журнал HTTP-запросов провайдера

```powershell
$env:PLATFORMCRAFT_HTTP_DEBUG = "1"; $env:TF_LOG = "DEBUG"; $env:TF_LOG_PATH = ".\terraform-debug.log"
terraform plan -var="bucket_name=$env:BUCKET"
```

```bash
export PLATFORMCRAFT_HTTP_DEBUG=1 TF_LOG=DEBUG TF_LOG_PATH=./terraform-debug.log
terraform plan -var="bucket_name=$BUCKET"
```

В журнале у каждого ответа видны статус, `Date` и все заголовки, включая
`x-amz-request-id`. Выключение:
`Remove-Item Env:PLATFORMCRAFT_HTTP_DEBUG, Env:TF_LOG, Env:TF_LOG_PATH` (PowerShell)
или `unset PLATFORMCRAFT_HTTP_DEBUG TF_LOG TF_LOG_PATH` (bash).

Для acceptance-тестов журнал включается так же: переменные окружения
действуют и на `go test`.

### 6.3. Что приложить к обращению в поддержку PlatformCraft

- текст ошибки из вывода Terraform или теста;
- фрагмент журнала из раздела 6.2 с проблемным запросом: метод, URL, статус,
  заголовки ответа и `x-amz-request-id`;
- время запроса (в журнале — в UTC) и имя бакета.
