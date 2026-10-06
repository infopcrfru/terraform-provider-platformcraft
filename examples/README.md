# Примеры

| Каталог | Содержание |
|---|---|
| `complete/` | Полная конфигурация: все ресурсы, data sources и ephemeral-ресурс. Сценарий проверки на ней — [TESTING.md](../TESTING.md) |
| `provider/` | Подключение провайдера (главная страница документации) |
| `provider-credentials/` | Передача ключей через переменные Terraform |
| `resources/<ресурс>/` | `resource.tf` — пример ресурса, `import.sh` — команда импорта |
| `data-sources/<data source>/` | `data-source.tf` — пример data source |
| `ephemeral-resources/<ресурс>/` | `ephemeral-resource.tf` — пример ephemeral-ресурса |

Файлы `provider/provider.tf`, `resources/*/resource.tf`, `resources/*/import.sh`,
`data-sources/*/data-source.tf` и `ephemeral-resources/*/ephemeral-resource.tf`
входят в документацию Registry (`docs/`). После их изменения перегенерируйте
документацию: `cd tools; go generate ./...`.

Примеры ресурсов и data sources — фрагменты: блок `terraform { required_providers }`
в них опущен. Чтобы запустить фрагмент отдельно, добавьте блок из
`provider/provider.tf`.
