package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/provider"
)

// version подставляется при сборке релиза через -ldflags "-X main.version=..."
// (см. .goreleaser.yml). При локальной сборке остаётся "dev".
var (
	version string = "dev"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "запустить провайдер с поддержкой отладчика (delve и т.п.)")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// Адрес в Terraform Registry: namespace infopcrfru (GitHub-аккаунт),
		// имя platformcraft (репозиторий terraform-provider-platformcraft).
		// Должен совпадать с source в required_providers и с ключом dev_overrides.
		Address: "registry.terraform.io/infopcrfru/platformcraft",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
