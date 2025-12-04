package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/raywall/terraform-provider-raysouz/provider/internal/models"
	dto "github.com/raywall/terraform-provider-raysouz/pkg/types"
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
						"runtime":       {Type: schema.TypeString, Required: true},
						"handler":       {Type: schema.TypeString, Required: true},
						"zip_file":      {Type: schema.TypeString, Required: true},
						"memory_size":   {Type: schema.TypeInt, Optional: true, Default: 128},
						"timeout":       {Type: schema.TypeInt, Optional: true, Default: 30},
						"attached_policy_arns": {
							Type:        schema.TypeList,
							Optional:    true,
							Description: "Lista de ARNs de políticas gerenciadas para anexar à Role de execução da Lambda.",
							Elem:        &schema.Schema{Type: schema.TypeString},
						},
						"environment_variables": {
							Type:     schema.TypeMap,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
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
	// 1. Acesso ao ConfigurationBundle (CORRIGIDO: usa 'raysouz' como alias)
	bundle, ok := m.(*models.ConfigurationBundle)
	if !ok || bundle.DeployService == nil {
		return diag.FromErr(fmt.Errorf("deployment service not configured"))
	}

	// 2. Mapeamento de Entrada (Schema -> DTOs)
	apiID := d.Get("api_gateway_id").(string)
	stage := d.Get("stage_name").(string)

	lc, routes := extractConfig(d) // lc e routes são tipos 'types.'

	// 3. Executa a Lógica (Chama o Service)
	state, err := bundle.DeployService.EnsureDeployment(ctx, extractAPIID(apiID), stage, lc, routes)
	if err != nil {
		return diag.FromErr(fmt.Errorf("deployment failed: %w", err))
	}

	// 4. Persistência de Saída (DTO -> Internal State)
	d.SetId(fmt.Sprintf("%s/%s", state.APIGatewayID, state.FunctionName))
	b, _ := json.Marshal(state)
	_ = d.Set("internal", string(b))

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

    // CORREÇÃO: Chama o Service Facade para verificar o estado completo
    exists, err := bundle.DeployService.CheckResourceExistence(ctx, &st)
    if err != nil {
        // Se houver erro de comunicação/API, retorna diag.FromErr
        return diag.FromErr(fmt.Errorf("failed during existence check: %w", err))
    }

    if !exists {
        // Se o Service diz que não existe (Role ou Lambda), marca como drift
        d.SetId("")
    }
    
    return nil
}

// resourceUpdate (Controller)
func resourceUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	return resourceCreate(ctx, d, m)
}

// resourceDelete (Controller) - Chama o Service para limpar
func resourceDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	bundle, ok := m.(*models.ConfigurationBundle)
	if !ok || bundle.DeployService == nil {
		return diag.FromErr(fmt.Errorf("deployment service not configured"))
	}

	// 1. Recupera o estado
	internal := d.Get("internal").(string)
	if internal == "" {
		d.SetId("")
		return nil
	}

	var st dto.ResourceState
	if err := json.Unmarshal([]byte(internal), &st); err != nil {
		return diag.FromErr(err)
	}

	// 2. Chama o método de serviço para limpar todos os recursos
	if err := bundle.DeployService.DeleteDeployment(ctx, &st); err != nil {
		return diag.FromErr(fmt.Errorf("failed to delete deployment: %w", err))
	}

	d.SetId("")
	return nil
}

// extractAPIID (Função auxiliar, movida para aqui ou para outro utilitário)
// DEFINIÇÃO MOVIDA PARA ESTE ARQUIVO (já que era auxiliar)
func extractAPIID(apiID string) string {
	parts := strings.Split(apiID, ":")
	if len(parts) > 1 {
		return parts[1]
	}
	return apiID
}

// extractConfig extrai os dados do schema para DTOs do Service.
func extractConfig(d *schema.ResourceData) (*dto.LambdaConfig, []dto.RouteConfig) {
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

	lc := &dto.LambdaConfig{
		FunctionName: lcMap["function_name"].(string),
		Runtime:      lcMap["runtime"].(string),
		Handler:      lcMap["handler"].(string),
		ZipPath:      lcMap["zip_file"].(string),
		MemorySize:   int32(lcMap["memory_size"].(int)),
		Timeout:      int32(lcMap["timeout"].(int)),
		PolicyARNs:   policyARNs,
		Environment:  env,
	}

	routesRaw := d.Get("routes").([]interface{})
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

	return lc, routes
}
