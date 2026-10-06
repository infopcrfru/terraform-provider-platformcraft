# Terraform Provider PlatformCraft

Terraform-провайдер для [S3-хранилища PlatformCraft](https://doc.platformcraft.ru/s3/)
(S3-совместимое API, эндпоинт по умолчанию `https://eu-s3.platformcraft.com`,
регион `eu-central-2`).

- Terraform Registry: [infopcrfru/platformcraft](https://registry.terraform.io/providers/infopcrfru/platformcraft/latest)
- Документация ресурсов: [docs/](docs/) (та же, что на странице провайдера в Registry)

| Документ | Содержание |
|---|---|
| [README.md](README.md) | Установка, настройка, ресурсы, особенности работы |
| [examples/](examples/) | Примеры конфигураций |
| [TESTING.md](TESTING.md) | Ручной сценарий проверки и автотесты (модульные и acceptance) |
| [CHANGELOG.md](CHANGELOG.md) | Изменения по версиям |

---

## 1. Установка и настройка

### 1.1. Подключение

Требования: Terraform 1.0 или новее; ephemeral-ресурсы (presigned URL) —
Terraform 1.10 или новее.

```hcl
terraform {
  required_providers {
    platformcraft = {
      source  = "infopcrfru/platformcraft"
      version = "~> 0.1"
    }
  }
}

provider "platformcraft" {}
```

```powershell
terraform init
```

Terraform скачает провайдер из Registry и проверит его подпись.

### 1.2. Ключи доступа

Ключи создаются в личном кабинете PlatformCraft: **Настройки → Доступы S3 →
Сгенерировать**. Храните их в переменных окружения, а не в `.tf`:

```powershell
$env:PLATFORMCRAFT_ACCESS_KEY = "<access key>"
$env:PLATFORMCRAFT_SECRET_KEY = "<secret key>"
```

```bash
export PLATFORMCRAFT_ACCESS_KEY="<access key>"
export PLATFORMCRAFT_SECRET_KEY="<secret key>"
```

Переменные `$env:` в PowerShell действуют только в текущем окне.

### 1.3. Параметры провайдера

```hcl
provider "platformcraft" {
  # Все параметры необязательны.
  # endpoint   = "https://eu-s3.platformcraft.com"
  # region     = "eu-central-2"
  # access_key = "..."
  # secret_key = "..."
  # max_requests_per_second = 50
}
```

| Параметр | Переменная окружения | По умолчанию |
|---|---|---|
| `endpoint` | `PLATFORMCRAFT_ENDPOINT` | `https://eu-s3.platformcraft.com` |
| `region` | `PLATFORMCRAFT_REGION` | `eu-central-2` |
| `access_key` | `PLATFORMCRAFT_ACCESS_KEY` | — |
| `secret_key` | `PLATFORMCRAFT_SECRET_KEY` | — |
| `max_requests_per_second` | `PLATFORMCRAFT_MAX_RPS` | `50` (`0` — без ограничения) |

- Значение в блоке `provider` имеет приоритет над переменной окружения.
- Ключи задаются парой: если указан только один из них, провайдер завершается
  с ошибкой конфигурации.
- Если не задан ни один ключ, используется стандартная цепочка AWS SDK
  (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`, `~/.aws/credentials`).

### 1.4. Журнал HTTP-запросов

`PLATFORMCRAFT_HTTP_DEBUG=1` включает журналирование каждого запроса и ответа
AWS SDK (метод, URL, заголовки, в том числе `x-amz-request-id`). Terraform
записывает его в свой лог:

```powershell
$env:PLATFORMCRAFT_HTTP_DEBUG = "1"; $env:TF_LOG = "DEBUG"; $env:TF_LOG_PATH = ".\terraform-debug.log"
terraform plan
```

```bash
export PLATFORMCRAFT_HTTP_DEBUG=1 TF_LOG=DEBUG TF_LOG_PATH=./terraform-debug.log
terraform plan
```

Секретный ключ в журнал не попадает (в заголовке `Authorization` только Access
Key ID и подпись), тела запросов и ответов не записываются. По журналу видно
точное время, статус и заголовки каждого ответа — он пригодится при обращении
в поддержку PlatformCraft.

---

## 2. Ресурсы, data sources и ephemeral-ресурсы

Полное описание аргументов и атрибутов — в [docs/](docs/) и на странице
провайдера в Registry.

### Ресурсы (10)

| Ресурс | Назначение | ID для импорта |
|---|---|---|
| `platformcraft_bucket` | Бакет; `force_destroy` очищает бакет перед удалением | `<bucket>` |
| `platformcraft_bucket_versioning` | Версионирование (`Enabled`/`Suspended`) | `<bucket>` |
| `platformcraft_bucket_acl` | Canned ACL бакета | `<bucket>` |
| `platformcraft_bucket_policy` | Bucket policy (JSON) | `<bucket>` |
| `platformcraft_bucket_cors` | CORS, одно или несколько правил `rule` | `<bucket>` |
| `platformcraft_bucket_object_lock_configuration` | Object Lock: режим и срок retention по умолчанию | `<bucket>` |
| `platformcraft_object` | Объект: `content` или `source_path`, ACL | `<bucket>,<key>` |
| `platformcraft_object_copy` | Серверная копия объекта (copy-object) | `<bucket>,<key>` назначения |
| `platformcraft_object_retention` | Retention объекта (`GOVERNANCE`/`COMPLIANCE`) | `<bucket>,<key>` |
| `platformcraft_object_legal_hold` | Legal hold объекта | `<bucket>,<key>` |

В составном ID разделитель — запятая: ключ объекта сам может содержать `/`.

### Data sources (12)

| Data source | Что возвращает |
|---|---|
| `platformcraft_buckets` | Имена всех бакетов аккаунта |
| `platformcraft_bucket` | Статус версионирования, количество и суммарный размер объектов |
| `platformcraft_bucket_versioning` | Статус версионирования |
| `platformcraft_bucket_acl` | Владелец и гранты ACL бакета |
| `platformcraft_bucket_policy` | Bucket policy (JSON) |
| `platformcraft_bucket_cors` | CORS-правила |
| `platformcraft_bucket_object_lock_configuration` | Режим и срок Object Lock по умолчанию |
| `platformcraft_bucket_objects` | Список объектов (list-objects-v2, с пагинацией) |
| `platformcraft_bucket_objects_v1` | Список объектов (list-objects v1, с пагинацией) |
| `platformcraft_object` | Метаданные объекта; содержимое (`read_content`) или скачивание в файл (`download_path`) |
| `platformcraft_object_acl` | Владелец и гранты ACL объекта |
| `platformcraft_object_versions` | Версии объектов и delete-маркеры (с пагинацией) |

### Ephemeral-ресурсы (2, Terraform 1.10+)

| Ephemeral-ресурс | Назначение |
|---|---|
| `platformcraft_object_presigned_get_url` | Presigned URL на скачивание объекта |
| `platformcraft_object_presigned_put_url` | Presigned URL на загрузку объекта |

Presigned URL — секрет с ограниченным сроком действия, поэтому он реализован
как ephemeral-ресурс: значение не сохраняется ни в plan, ни в state.

### Не выделены в отдельные ресурсы

- **Multipart upload** — используется внутри `platformcraft_object`
  автоматически для больших файлов (`manager.Uploader` из AWS SDK), собственного
  жизненного цикла в Terraform у него нет.
- **DeleteObjects (пакетное удаление)** — Terraform и так удаляет несколько
  `platformcraft_object` параллельно.
- **Ownership controls** — PlatformCraft не поддерживает эту операцию. Для ACL
  она не нужна: `put-bucket-acl`/`put-object-acl` работают без предварительной
  настройки.
- **Шифрование и lifecycle-правила** — в PlatformCraft не поддерживаются.

---

## 3. Примеры

| Путь | Содержание |
|---|---|
| [examples/complete/main.tf](examples/complete/main.tf) | Полная конфигурация: все 10 ресурсов, все 12 data sources и ephemeral-ресурс |
| `examples/resources/<ресурс>/` | Пример ресурса (`resource.tf`) и команда импорта (`import.sh`) |
| `examples/data-sources/<data source>/` | Пример data source |
| `examples/ephemeral-resources/<ресурс>/` | Пример ephemeral-ресурса |
| `examples/provider/`, `examples/provider-credentials/` | Подключение и настройка провайдера |

Примеры из `resources/`, `data-sources/` и `ephemeral-resources/` входят в
документацию Registry.

Имя бакета уникально во всей системе PlatformCraft, поэтому полный пример
запускается со своим именем:

```powershell
cd examples\complete
$env:BUCKET = "my-unique-test-bucket-123"
terraform init
terraform apply -var="bucket_name=$env:BUCKET"
terraform destroy -var="bucket_name=$env:BUCKET"
```

```bash
cd examples/complete
terraform init
terraform apply -var="bucket_name=my-unique-test-bucket-123"
terraform destroy -var="bucket_name=my-unique-test-bucket-123"
```

В PowerShell используйте именно `$env:BUCKET`: `$BUCKET` — другая, обычная
переменная, и если она не задана, Terraform получит пустое имя бакета.

Пошаговый сценарий проверки (идемпотентность, дрифт, импорт) — в
[TESTING.md](TESTING.md).

---

## 4. Особенности работы

### Object Lock

- **Object Lock нельзя выключить.** Ни S3, ни PlatformCraft не позволяют снять
  Object Lock с бакета. Удаление `platformcraft_bucket_object_lock_configuration`
  выдаёт предупреждение и убирает ресурс из state; на бакете конфигурация остаётся.
- **Версионирование на бакете с Object Lock нельзя приостановить.** Удаление
  `platformcraft_bucket_versioning` на таком бакете выдаёт предупреждение
  вместо ошибки `InvalidBucketState`.
- **Retention по умолчанию применяется ко всем новым объектам бакета**, в том
  числе к копиям. Чтобы `destroy` мог удалить такие объекты, задайте
  `bypass_governance_retention = true` у `platformcraft_object` и
  `platformcraft_object_copy` (работает только для режима GOVERNANCE).
- **COMPLIANCE нельзя снять досрочно** — никому, включая владельца аккаунта.
- **Снятие GOVERNANCE-retention при удалении ресурса.** PlatformCraft не
  принимает `retain_until` в прошлом, поэтому при удалении
  `platformcraft_object_retention` срок переносится на «сейчас + 60 секунд».
- **Legal hold копии.** Копия может унаследовать legal hold источника.
  `platformcraft_object_copy` при удалении проверяет legal hold копии и снимает
  его, если он включён.
- **`force_destroy` у `platformcraft_bucket`** удаляет все объекты, версии и
  delete-маркеры (с обходом GOVERNANCE и снятием legal hold) перед удалением
  бакета. Версии под COMPLIANCE удалить нельзя — удаление бакета вернёт ошибку.

### Чтение состояния и дрифт

- **`platformcraft_object` по умолчанию не скачивает содержимое при чтении.**
  Изменения видны по ETag. `refresh_content = true` включает скачивание и
  сравнение содержимого (только для `content`, не для `source_path`).
- **Изменение локального файла `source_path` без смены пути** не обнаруживается:
  провайдер не хранит хеш файла. Чтобы перезагрузить объект, поменяйте путь
  или используйте `terraform apply -replace=...`.
- **Размер содержимого в state ограничен 64 МиБ** (`content`,
  `refresh_content`, `read_content`). Протокол Terraform ↔ плагин ограничивает
  сообщение ~256 МиБ; для больших объектов используйте `download_path` у data
  source `platformcraft_object`, он пишет файл на диск потоково.
- **`platformcraft_bucket_acl`: дрифт по `acl` не отслеживается.** API
  возвращает список грантов, а не исходный canned ACL, и однозначно
  восстановить одно из другого нельзя. Фактические гранты доступны через data
  sources `platformcraft_bucket_acl`/`platformcraft_object_acl`.
- **`platformcraft_bucket_policy` сравнивает политику по смыслу**, а не как
  строку: порядок ключей, форматирование, `"*"` против `{"AWS": ["*"]}` и
  одиночное значение против массива из одного элемента не дают диффа.
- **После импорта** `platformcraft_object` не содержит `content`/`source_path`,
  а `platformcraft_object_copy` — `source_bucket`/`source_key`: API их не
  возвращает. Заполните их в конфигурации, иначе следующий `apply` перезапишет
  объект.

### Data sources

- **Data source читается при каждом `plan`**, если его входные значения
  известны. `download_path` при этом каждый раз заново скачивает файл —
  провайдер выводит об этом предупреждение.
- **`depends_on` для настроек бакета.** Data source, который ссылается только
  на имя бакета, не зависит от ресурса, который эту настройку пишет. Без
  `depends_on` он может прочитать состояние до применения ресурса.
  В `examples/complete/main.tf` зависимости указаны.
- **Ephemeral-значения нельзя вывести через `output` корневого модуля** —
  ограничение языка Terraform.

### Особенности API, которые учитывает провайдер

- **Presigned URL без параметра `x-id`.** AWS SDK добавляет в каждую
  presigned-ссылку служебный параметр `x-id`, а PlatformCraft принимает ссылки
  только без него. Провайдер удаляет параметр до подписи. Если строите presigned
  URL собственными средствами на aws-sdk-go-v2, делайте то же самое.
- **Бакет без политики.** Для бакета, политика которого удалена,
  get-bucket-policy возвращает документ `{"Version":"","Statement":null}`.
  Провайдер считает такой документ отсутствием политики; data source
  `platformcraft_bucket_policy` возвращает для него пустую строку.

### Лимиты API

Лимиты PlatformCraft по умолчанию: 70 запросов в секунду и 240 Мбит/с на
клиента (расширяются по запросу). При превышении частоты API отвечает `429`.

- Провайдер отправляет не больше `max_requests_per_second` запросов в секунду
  (по умолчанию 50 — с запасом для других программ под теми же ключами).
  Ограничение действует на весь процесс провайдера, включая повторы.
- Ответ `429` повторяется до 6 попыток с нарастающей паузой (до 20 с).
- Лимит общий для всех программ с теми же ключами: Terraform, aws-cli,
  скрипты. Если рядом идёт интенсивная работа, уменьшите
  `max_requests_per_second`; если лимит расширен — увеличьте.
- При полосе 240 Мбит/с объект 1 ГБ загружается не быстрее чем за ~35 с.
  Долгий `apply` с большим `source_path` — не зависание.

### Версия AWS SDK

`aws-sdk-go-v2/service/s3` закреплён на v1.61.0. Начиная с v1.73.0 SDK по
умолчанию считает контрольные суммы CRC32 и передаёт тело загрузки в формате
`aws-chunked`. Перед обновлением SDK прогоните acceptance-тесты; если загрузки
перестанут проходить, задайте в клиенте
`RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired` и
`ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired`.

---

## 5. Разработка

Требования: Go 1.25.8+, Terraform CLI 1.10+, Git.

### 5.1. Сборка и локальный запуск

```powershell
git clone https://github.com/infopcrfru/terraform-provider-platformcraft.git
cd terraform-provider-platformcraft
go build -o terraform-provider-platformcraft.exe .
```

```bash
go build -o terraform-provider-platformcraft .
```

Чтобы Terraform использовал локальную сборку вместо версии из Registry,
добавьте `dev_overrides` в файл настроек Terraform CLI:
`%APPDATA%\terraform.rc` в Windows (обычно
`C:\Users\<пользователь>\AppData\Roaming\terraform.rc`) или `~/.terraformrc`
в Linux/macOS:

```hcl
provider_installation {
  dev_overrides {
    "infopcrfru/platformcraft" = "C:/Users/<пользователь>/src/terraform-provider-platformcraft"
  }
  direct {}
}
```

Путь указывает на папку с собранным бинарником, а не на сам файл; в Windows —
с прямыми слэшами. Ключ `infopcrfru/platformcraft` совпадает с `source` в
`required_providers`. При активном `dev_overrides` Terraform выводит
предупреждение `Provider development overrides are in effect` — это ожидаемо;
ограничение `version` для этого провайдера не действует. Чтобы вернуться
к версии из Registry, удалите блок `dev_overrides`.

### 5.2. Проверки

```powershell
gofmt -l .                     # должно быть пусто
go vet ./...
go build ./...
go test ./...                  # модульные тесты
```

Acceptance-тесты работают с настоящим API и создают реальные бакеты — порядок
запуска описан в [TESTING.md](TESTING.md), раздел 5.2.

### 5.3. Документация

Каталог `docs/` генерируется утилитой
[tfplugindocs](https://github.com/hashicorp/terraform-plugin-docs) из описаний
схемы в коде (`Description`), примеров в `examples/` и шаблона
`templates/index.md.tmpl`. Вручную `docs/` не редактируется. После изменения
схемы или примеров:

```powershell
cd tools
go generate ./...
```

Нужен Terraform CLI в `PATH`. CI проверяет, что `docs/` соответствует коду.

### 5.4. Структура репозитория

```
main.go                       точка входа плагина
internal/provider/            ресурсы, data sources, ephemeral-ресурсы, тесты
internal/pcs3/                обёртки над S3 API, ограничение частоты, повторы
examples/                     примеры (используются и в документации)
templates/index.md.tmpl       шаблон главной страницы документации
docs/                         документация для Terraform Registry (генерируется)
tools/                        генерация документации (go generate)
terraform-registry-manifest.json  версия протокола для Registry
.goreleaser.yml               сборка релиза
.github/workflows/            CI, acceptance-тесты, релиз
```

---

## 6. Лицензия

[Mozilla Public License 2.0](LICENSE).
