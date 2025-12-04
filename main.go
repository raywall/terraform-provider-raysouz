package main

import (
	"context"
	"flag"
	"log"

	raysouz "github.com/raywall/terraform-provider-raysouz/provider" // Aponta para o provider/provider.go

	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	var debugMode bool

	flag.BoolVar(&debugMode, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := &plugin.ServeOpts{
		ProviderFunc: raysouz.Provider,
	}

	if debugMode {
		err := plugin.Debug(context.Background(), "registry.terraform.io/raywall/raysouz", opts)
		if err != nil {
			log.Fatal(err.Error())
		}
		return
	}

	plugin.Serve(opts)
}
