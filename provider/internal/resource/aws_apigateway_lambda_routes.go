package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	dto "github.com/raywall/terraform-provider-raysouz/pkg/types"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/models"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/service"
)

// ResourceAPIGatewayLambdaRoutes define o schema do recurso.
func ResourceAPIGatewayLambdaRoutes() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceCreate,
		ReadContext:   resourceRead,
		UpdateContext: resourceUpdate,
		DeleteContext: resourceDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"api_gateway_id": {Type: schema.TypeString, Required: true},
			"stage_name":     {Type: schema.TypeString, Required: true},
			"lambda_config": {
				Type:     schema.TypeList,
				MaxItems: 1,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"function_name": {Type: schema.TypeString, Required: true},
						"handler":       {Type: schema.TypeString, Required: true},
						"runtime":       {Type: schema.TypeString, Required: true},
						"timeout":       {Type: schema.TypeInt, Optional: true, Default: 30},
						"memory_size":   {Type: schema.TypeInt, Optional: true, Default: 128},
						"zip_file":      {Type: schema.TypeString, Optional: true},
						"s3_bucket":     {Type: schema.TypeString, Optional: true},
						"s3_key":        {Type: schema.TypeString, Optional: true},
						"force_update": {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "Força a atualização do código Lambda mesmo sem mudanças detectadas",
						},
						"environment_variables": {
							Type:     schema.TypeMap,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"attached_policy_arns": {
							Type:        schema.TypeList,
							Optional:    true,
							Description: "Lista de ARNs de políticas gerenciadas para anexar à Role de execução da Lambda.",
							Elem:        &schema.Schema{Type: schema.TypeString},
						},
					},
				},
			},
			"routes": {
				Type:     schema.TypeList,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"path":          {Type: schema.TypeString, Required: true},
						"method":        {Type: schema.TypeString, Required: true},
						"authorization": {Type: schema.TypeString, Optional: true, Default: "NONE"},
						"authorizer_id": {Type: schema.TypeString, Optional: true},
					},
				},
			},
			"internal": {Type: schema.TypeString, Computed: true},
		},
	}
}

// resourceCreate (Controller) - Mapeia e chama o Service
func resourceCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	// 1. Acesso ao ConfigurationBundle
	bundle, ok := m.(*models.ConfigurationBundle)
	if !ok || bundle.DeployService == nil {
		return diag.FromErr(fmt.Errorf("deployment service not configured"))
	}

	// 2. Mapeamento de Entrada (Schema -> DTOs)
	apiID := d.Get("api_gateway_id").(string)
	stage := d.Get("stage_name").(string)

	lc, routes, forceUpdate := extractConfig(d)

	// VALIDAÇÃO: deve ter zip_file OU (s3_bucket E s3_key)
	if err := validateLambdaSource(lc); err != nil {
		return diag.FromErr(err)
	}

	// 3. Executa a Lógica (Chama o Service)
	state, err := bundle.DeployService.EnsureDeployment(
		ctx, 
		extractAPIID(apiID), 
		stage, 
		lc, 
		routes, 
		forceUpdate,
		bundle.StateService, // Passar o StateService
	)
	if err != nil {
		return diag.FromErr(fmt.Errorf("deployment failed: %w", err))
	}

	// 4. Persistência de Saída (DTO -> Internal State)
	d.SetId(fmt.Sprintf("%s/%s", state.APIGatewayID, state.FunctionName))
	b, _ := json.Marshal(state)
	_ = d.Set("internal", string(b))

	return nil
}

// resourceUpdate (Controller) - Com gerenciamento de estado interno
func resourceUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	bundle, ok := m.(*models.ConfigurationBundle)
	if !ok || bundle.DeployService == nil || bundle.StateService == nil {
		return diag.FromErr(fmt.Errorf("services not configured"))
	}

	// Obter ID atual
	resourceID := d.Id()
	if resourceID == "" {
		return resourceCreate(ctx, d, m)
	}

	// Extrair configurações
	apiID := d.Get("api_gateway_id").(string)
	stage := d.Get("stage_name").(string)
	lc, routes, forceUpdate := extractConfig(d)

	// Validar
	if err := validateLambdaSource(lc); err != nil {
		return diag.FromErr(err)
	}

	// Executar deployment com gerenciamento de estado
	state, err := bundle.DeployService.EnsureDeployment(
		ctx, 
		extractAPIID(apiID), 
		stage, 
		lc, 
		routes, 
		forceUpdate,
		bundle.StateService, // Passar o StateService
	)
	if err != nil {
		return diag.FromErr(fmt.Errorf("deployment update failed: %w", err))
	}

	// Atualizar estado no Terraform
	d.SetId(fmt.Sprintf("%s/%s", state.APIGatewayID, state.FunctionName))
	b, _ := json.Marshal(state)
	_ = d.Set("internal", string(b))

	return nil
}

// resourceDelete (Controller) - Chama o Service para limpar
func resourceDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	bundle, ok := m.(*models.ConfigurationBundle)
	if !ok || bundle.DeployService == nil {
		return diag.FromErr(fmt.Errorf("deployment service not configured"))
	}

	internal := d.Get("internal").(string)
	if internal == "" {
		d.SetId("")
		return nil
	}

	var st dto.ResourceState
	if err := json.Unmarshal([]byte(internal), &st); err != nil {
		return diag.FromErr(err)
	}

	// Passar stateService para limpar estado interno
	var stateService service.StateServiceInterface
	if bundle.StateService != nil {
		stateService = bundle.StateService
	}

	if err := bundle.DeployService.DeleteDeployment(ctx, &st, stateService); err != nil {
		return diag.FromErr(fmt.Errorf("failed to delete deployment: %w", err))
	}

	d.SetId("")
	return nil
}

// resourceRead (Controller)
func resourceRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	bundle, ok := m.(*models.ConfigurationBundle)
	if !ok || bundle.DeployService == nil {
		return diag.FromErr(fmt.Errorf("deployment service not configured"))
	}

	internal := d.Get("internal").(string)
	if internal == "" {
		return nil
	}

	var st dto.ResourceState
	if err := json.Unmarshal([]byte(internal), &st); err != nil {
		d.SetId("")
		return diag.FromErr(fmt.Errorf("failed reading internal state: %w", err))
	}

	exists, err := bundle.DeployService.CheckResourceExistence(ctx, &st)
	if err != nil {
		return diag.FromErr(fmt.Errorf("failed during existence check: %w", err))
	}

	if !exists {
		d.SetId("")
	}

	return nil
}

// validateLambdaSource valida se a origem do código Lambda é válida
func validateLambdaSource(lc *dto.LambdaConfig) error {
	hasZipFile := lc.ZipPath != ""
	hasS3Source := lc.S3Bucket != "" && lc.S3Key != ""

	if !hasZipFile && !hasS3Source {
		return fmt.Errorf("lambda_config must have either 'zip_file' or both 's3_bucket' and 's3_key'")
	}

	if hasZipFile && hasS3Source {
		return fmt.Errorf("lambda_config cannot have both 'zip_file' and S3 source ('s3_bucket'/'s3_key')")
	}

	if lc.S3Bucket != "" && lc.S3Key == "" {
		return fmt.Errorf("when 's3_bucket' is provided, 's3_key' must also be provided")
	}

	if lc.S3Bucket == "" && lc.S3Key != "" {
		return fmt.Errorf("when 's3_key' is provided, 's3_bucket' must also be provided")
	}

	return nil
}

// extractAPIID extrai o ID da API do formato completo
func extractAPIID(apiID string) string {
	parts := strings.Split(apiID, ":")
	if len(parts) > 1 {
		return parts[1]
	}
	return apiID
}

// extractConfig extrai os dados do schema para DTOs do Service.
func extractConfig(d *schema.ResourceData) (*dto.LambdaConfig, []dto.RouteConfig, bool) {
	lcList := d.Get("lambda_config").([]interface{})
	lcMap := lcList[0].(map[string]interface{})

	envRaw := lcMap["environment_variables"].(map[string]interface{})
	env := make(map[string]string)
	for k, v := range envRaw {
		env[k] = v.(string)
	}

	policyARNsRaw := lcMap["attached_policy_arns"].([]interface{})
	policyARNs := make([]string, len(policyARNsRaw))
	for i, p := range policyARNsRaw {
		policyARNs[i] = p.(string)
	}

	forceUpdate := false
	if fu, ok := lcMap["force_update"]; ok {
		forceUpdate = fu.(bool)
	}

	lc := &dto.LambdaConfig{
		FunctionName: lcMap["function_name"].(string),
		Handler:      lcMap["handler"].(string),
		Runtime:      lcMap["runtime"].(string),
		Timeout:      int32(lcMap["timeout"].(int)),
		MemorySize:   int32(lcMap["memory_size"].(int)),
		ZipPath:      lcMap["zip_file"].(string),
		S3Bucket:     lcMap["s3_bucket"].(string),
		S3Key:        lcMap["s3_key"].(string),
		Environment:  env,
		PolicyARNs:   policyARNs,
		ForceUpdate:  forceUpdate,
	}

	routesRaw := d.Get("routes").([]interface{})
	routes := extractRoutesFromRaw(routesRaw)

	return lc, routes, forceUpdate
}

// extractRoutesFromRaw extrai rotas de dados brutos
func extractRoutesFromRaw(routesRaw []interface{}) []dto.RouteConfig {
	routes := make([]dto.RouteConfig, 0, len(routesRaw))

	for _, r := range routesRaw {
		rm := r.(map[string]interface{})
		routes = append(routes, dto.RouteConfig{
			Path:          rm["path"].(string),
			Method:        strings.ToUpper(rm["method"].(string)),
			Authorization: rm["authorization"].(string),
			AuthorizerID:  fmt.Sprintf("%v", rm["authorizer_id"]),
		})
	}

	return routes
}
