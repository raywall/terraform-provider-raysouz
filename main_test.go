package main

import (
	"io/ioutil"
	providers "terraform-provider-raysouz/providers/raysouz"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func readTestConfigFile(t *testing.T, filename string) string {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Fatalf("Erro ao ler o arquivo de configuração: %v", err)
	}
	return string(content)
}

func TestCustomProviderIntegration(t *testing.T) {
	// Definir o arquivo de configuração Terraform a ser utilizado
	tfConfig := readTestConfigFile(t, `testdata/main.tf`)

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

// func TestCustomProviderConfigure(t *testing.T) {
// 	p := &schema.Provider{}
// 	resourceData := schema.TestResourceDataRaw(t, schema.NewSet(schema.HashString, nil), nil)

// 	_, err := p.Configure(resourceData)

// 	if err != nil {
// 		t.Errorf("Erro ao configurar o provedor: %v", err)
// 	}

// 	// Adicione mais verificações conforme necessário
// }
