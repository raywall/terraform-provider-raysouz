package raysouz

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/resource"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/service"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/models"
)

// Provider retorna o schema e resources map.
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_REGION", "us-east-1"),
				Description: "AWS region to use for resources",
			},
			"state_bucket": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Bucket S3 para armazenar backups de estado (statefile) para rollback.",
			},
			"rollback": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Se true, restaura o estado de rollback anterior antes de qualquer operação.",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			// Mapeamento para o recurso Lambda/APIGateway
			"raysouz_apigateway_lambda_routes": resource.ResourceAPIGatewayLambdaRoutes(),
			// Incluindo o placeholder ResourceCustom() para compilação, se existir.
			// "raysouz_custom_resource": resource.ResourceCustom(),
		},
		ConfigureContextFunc: providerConfigure,
	}
}

func providerConfigure(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	var diags diag.Diagnostics
	region := d.Get("region").(string)
	s3Bucket := d.Get("state_bucket").(string)
	doRollback := d.Get("rollback").(bool)

	// 1. Inicializa o AWS Client (Base)
	awsClient, err := client.New(ctx, region)
	if err != nil {
		diags = append(diags, diag.FromErr(fmt.Errorf("failed to create aws client: %w", err))...)
		return nil, diags
	}
	awsClient.S3Bucket = s3Bucket

	// 2. Inicializa os Repositórios (Camada de Acesso a Dados)
	iamRepo := &repository.IAMRepository{Client: awsClient}
	lambdaRepo := &repository.LambdaRepository{Client: awsClient}
	apigwRepo := &repository.APIGWRepository{Client: awsClient}
	cwLogsRepo := &repository.CWLogsRepository{Client: awsClient}
	stateRepo := &repository.StateRepository{Client: awsClient}

	// 3. Inicializa os Services Especializados (Camada de Lógica de Negócio)
	iamService := &service.IAMService{IAMRepo: iamRepo}
	cwLogsService := &service.CWLogsService{CWLogsRepo: cwLogsRepo}
	apigwService := &service.APIGatewayService{APIGWRepo: apigwRepo, Client: awsClient}
	hclStateService := &service.HCLStateService{StateRepo: stateRepo}

	// 4. Inicializa o Service Orquestrador (Facade)
	deployService := &service.LambdaDeploymentService{
		IAMService:        iamService,
		CWLogsService:     cwLogsService,
		APIGatewayService: apigwService,
		LambdaRepo:        lambdaRepo,
		Client:            awsClient,
	}

	// 5. LÓGICA DE ROLLBACK (Executa o Service de Estado)
	if s3Bucket != "" {
		stateDiags := hclStateService.HandleStateOperation(ctx, doRollback)
		diags = append(diags, stateDiags...) // Adiciona warnings/erros do state à lista
	}

	// 6. Retorna o Bundle para os Resources
	return &models.ConfigurationBundle{DeployService: deployService, Client: awsClient}, diags
}
