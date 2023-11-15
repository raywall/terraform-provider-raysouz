package main

import (
	providers "terraform-provider-raysouz/providers/raysouz"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestCustomProviderIntegration(t *testing.T) {
	// Configurar o ambiente de teste

	// Definir o arquivo de configuração Terraform a ser utilizado
	tfConfig := `
	provider "terraform-provider-raysouz" {}
	
	resource "custom_resource" "example" {
		message = "Hello, Test!"
		cloud   = "aws"
	}
	`

	// Definir as verificações de estado após a aplicação
	checks := []resource.TestCheckFunc{
		// Adicionar verificações conforme necessário
	}

	// Definir os provedores a serem utilizados no teste
	testProviders := map[string]*schema.Provider{
		"terraform-provider-raysouz": providers.Provider(),
	}

	// Executar o teste de integração
	resource.Test(t, resource.TestCase{
		Providers: testProviders,
		Steps: []resource.TestStep{
			{
				Config: tfConfig,
				Check: resource.ComposeTestCheckFunc(
					checks...,
				),
			},
		},
	})
}
