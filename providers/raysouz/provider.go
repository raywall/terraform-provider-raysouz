package raysouz

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/raywall/terraform-provider-raysouz/internal/raysouz/client"
	"github.com/raywall/terraform-provider-raysouz/resources/raysouz"
)

// Provider returns the terraform provider schema and resources map.
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_REGION", "us-east-1"),
				Description: "AWS region to use for resources",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"raysouz_apigateway_lambda_routes": raysouz.ResourceAPIGatewayLambdaRoutes(),
			"raysouz_custom_resource":          raysouz.ResourceCustom(),
		},
		ConfigureContextFunc: providerConfigure,
	}
}

func providerConfigure(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	var diags diag.Diagnostics
	region := d.Get("region").(string)

	c, err := client.New(ctx, region)
	if err != nil {
		diags = append(diags, diag.FromErr(fmt.Errorf("failed to create aws client: %w", err))...)
		return nil, diags
	}
	return c, diags
}
