package main

import (
	providers "github.com/raywall/terraform-provider-raysouz/providers/raysouz"

	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: providers.Provider,
	})
}
