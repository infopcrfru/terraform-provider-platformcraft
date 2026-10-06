//go:build generate

package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Форматирование примеров, из которых собирается документация. Требует
// Terraform CLI в PATH.
//go:generate terraform fmt -recursive ../examples/

// Генерация документации для Terraform Registry (каталог docs/) из схем
// провайдера и примеров в examples/.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. --provider-name platformcraft
