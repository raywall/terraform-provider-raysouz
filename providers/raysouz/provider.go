package raysouz

import (
	"context"
	"fmt"

	"github.com/raywall/terraform-provider-raysouz/internal/raysouz/client"
	resources "github.com/raywall/terraform-provider-raysouz/resources/raysouz"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_REGION", "us-east-1"),
				Description: "AWS region",
			},
			"access_key": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_ACCESS_KEY_ID", ""),
				Description: "AWS access key",
			},
			"secret_key": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_SECRET_ACCESS_KEY", ""),
				Description: "AWS secret key",
			},
			"profile": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_PROFILE", ""),
				Description: "AWS profile name",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"raysouz_apigateway_lambda_routes": resources.ResourceAPIGatewayLambdaRoutes(),
			"raysouz_custom_resource":          resources.ResourceCustom(),
		},
		DataSourcesMap:       map[string]*schema.Resource{},
		ConfigureContextFunc: providerConfigure,
	}
}

func providerConfigure(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	region := d.Get("region").(string)
	accessKey := d.Get("access_key").(string)
	secretKey := d.Get("secret_key").(string)

	// Validate credentials
	if (accessKey == "" && secretKey != "") || (accessKey != "" && secretKey == "") {
		return nil, diag.FromErr(fmt.Errorf("both access_key and secret_key must be provided if one is set"))
	}

	// Create AWS client
	awsClient, err := client.NewAWSClient(ctx, region, accessKey, secretKey)
	if err != nil {
		return nil, diag.FromErr(fmt.Errorf("creating AWS client: %w", err))
	}

	return awsClient, nil
}
