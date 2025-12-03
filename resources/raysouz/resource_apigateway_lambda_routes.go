package raysouz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/aws/aws-sdk-go-v2/aws"
	apigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	iam "github.com/aws/aws-sdk-go-v2/service/iam"
	lambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/raywall/terraform-provider-raysouz/internal/raysouz/client"
)

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

type resourceState struct {
	RoleName           string                  `json:"role_name"`
	FunctionName       string                  `json:"function_name"`
	FunctionArn        string                  `json:"function_arn"`
	APIGatewayID       string                  `json:"api_gateway_id"`
	StageName          string                  `json:"resourceCreate"`
	Routes             []routeState            `json:"routes"`
	LogGroup           string                  `json:"log_group"`
	Resources          map[string]resourceInfo `json:"resources"`
	AttachedPolicyARNs []string                `json:"attached_policy_arns"` // NOVO CAMPO
}

type routeState struct {
	Path          string `json:"path"`
	Method        string `json:"method"`
	Authorization string `json:"authorization"`
	AuthorizerID  string `json:"authorizer_id"`
	ResourceID    string `json:"resource_id"`
}

type resourceInfo struct {
	ResourceID string `json:"resource_id"`
	PathPart   string `json:"path_part"`
}

func resourceCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	awsClient, ok := m.(*client.AWSClient)
	if !ok || awsClient == nil {
		return diag.FromErr(fmt.Errorf("aws client not configured"))
	}

	apiIDRaw := d.Get("api_gateway_id").(string)
	apiID := extractAPIID(apiIDRaw)
	stage := d.Get("stage_name").(string)

	lcList := d.Get("lambda_config").([]interface{})
	if len(lcList) == 0 {
		return diag.FromErr(fmt.Errorf("lambda_config is required"))
	}
	lc := lcList[0].(map[string]interface{})
	fnName := lc["function_name"].(string)
	handler := lc["handler"].(string)
	zipPath := lc["zip_file"].(string)
	mem := int32(lc["memory_size"].(int))
	timeout := int32(lc["timeout"].(int))
	runtime := lc["runtime"].(string)

	// Leitura das variáveis de ambiente
	envRaw := lc["environment_variables"].(map[string]interface{})
	env := make(map[string]string)
	for k, v := range envRaw {
		env[k] = v.(string)
	}

	// Leitura dos ARNs de políticas anexadas
	policyARNsRaw := lc["attached_policy_arns"].([]interface{})
	policyARNs := make([]string, len(policyARNsRaw))
	for i, p := range policyARNsRaw {
		policyARNs[i] = p.(string)
	}

	// 1) Ensure role exists
	roleName := fmt.Sprintf("%s-execution-role", fnName)
	// Chamada para ensureRole AGORA COM policyARNs
	roleArn, err := ensureRole(ctx, awsClient, roleName, policyARNs)
	if err != nil {
		return diag.FromErr(fmt.Errorf("ensure role: %w", err))
	}

	// Correção de propagação IAM (5s)
	fmt.Printf("Aguardando 5 segundos pela propagação da Role IAM: %s\n", roleName)
	time.Sleep(5 * time.Second)

	d.SetId(fmt.Sprintf("%s/%s", apiID, fnName))

	// 2) Ensure Lambda exists
	functionArn, err := ensureLambdaFunction(ctx, awsClient, fnName, roleArn, handler, runtime, zipPath, mem, timeout, env)
	if err != nil {
		return diag.FromErr(fmt.Errorf("ensure lambda: %w", err))
	}

	// 3) Ensure log group
	logGroup := fmt.Sprintf("/aws/lambda/%s", fnName)
	if err := awsClient.CreateLogGroupIfNotExists(ctx, logGroup, 14); err != nil {
		return diag.FromErr(fmt.Errorf("log group setup failed: %w", err))
	}

	// 4) Add Lambda permission
	region := awsClient.Config.Region
	accountID, err := getAccountID(ctx, awsClient)
	if err != nil {
		return diag.FromErr(fmt.Errorf("get account id: %w", err))
	}

	sourceArn := fmt.Sprintf("arn:aws:execute-api:%s:%s:%s/*/*/*", region, accountID, apiID)
	if err := addLambdaPermission(ctx, awsClient, fnName, apiID, sourceArn); err != nil {
		return diag.FromErr(fmt.Errorf("add lambda permission: %w", err))
	}

	// 5) Get root resource
	rootID, err := getRootResourceID(ctx, awsClient, apiID)
	if err != nil {
		return diag.FromErr(fmt.Errorf("get root resource: %w", err))
	}

	// 6) Create routes
	routesRaw := d.Get("routes").([]interface{})
	routes := make([]routeState, 0, len(routesRaw))
	resources := make(map[string]resourceInfo)

	for _, r := range routesRaw {
		rm := r.(map[string]interface{})
		path := rm["path"].(string)
		method := strings.ToUpper(rm["method"].(string))
		authorization := rm["authorization"].(string)
		authorizerID := fmt.Sprintf("%v", rm["authorizer_id"])

		// Create resource path
		resourceID, pathResources, err := ensureRESTAPIPath(ctx, awsClient, apiID, rootID, path)
		if err != nil {
			return diag.FromErr(fmt.Errorf("ensure path %s: %w", path, err))
		}

		for k, v := range pathResources {
			resources[k] = v
		}

		// Create method
		if err := createRESTAPIMethod(ctx, awsClient, apiID, resourceID, method, authorization, authorizerID); err != nil {
			return diag.FromErr(fmt.Errorf("create method %s %s: %w", method, path, err))
		}

		// Create integration
		if err := createRESTAPIIntegration(ctx, awsClient, apiID, resourceID, method, functionArn, region); err != nil {
			return diag.FromErr(fmt.Errorf("create integration %s %s: %w", method, path, err))
		}

		// Create method response
		if err := createMethodResponse(ctx, awsClient, apiID, resourceID, method); err != nil {
			return diag.FromErr(fmt.Errorf("create method response %s %s: %w", method, path, err))
		}

		// Create integration response
		if err := createIntegrationResponse(ctx, awsClient, apiID, resourceID, method); err != nil {
			return diag.FromErr(fmt.Errorf("create integration response %s %s: %w", method, path, err))
		}

		routes = append(routes, routeState{
			Path:          path,
			Method:        method,
			Authorization: authorization,
			AuthorizerID:  authorizerID,
			ResourceID:    resourceID,
		})
	}

	// 7) Deploy API
	if err := deployRESTAPI(ctx, awsClient, apiID, stage); err != nil {
		return diag.FromErr(fmt.Errorf("deploy api: %w", err))
	}

	// 8) Store state (SALVA OS ARNS)
	st := resourceState{
		RoleName:           roleName,
		FunctionName:       fnName,
		FunctionArn:        functionArn,
		APIGatewayID:       apiID,
		StageName:          stage,
		Routes:             routes,
		LogGroup:           logGroup,
		Resources:          resources,
		AttachedPolicyARNs: policyARNs,
	}
	b, _ := json.Marshal(st)
	_ = d.Set("internal", string(b))

	time.Sleep(500 * time.Millisecond)
	return diags
}

func resourceRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	awsClient, ok := m.(*client.AWSClient)
	if !ok || awsClient == nil {
		return diag.FromErr(fmt.Errorf("aws client not configured"))
	}

	internal := d.Get("internal").(string)
	if internal == "" {
		return diags
	}

	var st resourceState
	if err := json.Unmarshal([]byte(internal), &st); err != nil {
		d.SetId("")
		return diag.FromErr(fmt.Errorf("failed reading internal state: %w", err))
	}

	// Verify role
	if _, err := awsClient.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(st.RoleName)}); err != nil {
		d.SetId("")
		return diags
	}

	// Verify lambda
	if _, err := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(st.FunctionName)}); err != nil {
		d.SetId("")
		return diags
	}

	return diags
}

func resourceUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	awsClient, ok := m.(*client.AWSClient)
	if !ok || awsClient == nil {
		return diag.FromErr(fmt.Errorf("aws client not configured"))
	}

	if d.HasChange("routes") {
		internal := d.Get("internal").(string)
		if internal != "" {
			var st resourceState
			if err := json.Unmarshal([]byte(internal), &st); err == nil {
				// Delete old methods
				for _, route := range st.Routes {
					if route.ResourceID != "" {
						_, _ = awsClient.APIGW.DeleteMethod(ctx, &apigw.DeleteMethodInput{
							RestApiId:  aws.String(st.APIGatewayID),
							ResourceId: aws.String(route.ResourceID),
							HttpMethod: aws.String(route.Method),
						})
					}
				}
			}
		}
	}

	return resourceCreate(ctx, d, m)
}

func resourceDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	awsClient, ok := m.(*client.AWSClient)
	if !ok || awsClient == nil {
		return diag.FromErr(fmt.Errorf("aws client not configured"))
	}

	internal := d.Get("internal").(string)
	if internal == "" {
		d.SetId("")
		return diags
	}

	var st resourceState
	if err := json.Unmarshal([]byte(internal), &st); err != nil {
		return diag.FromErr(err)
	}

	// 1. Delete methods first (required before deleting resources)
	for _, route := range st.Routes {
		if route.ResourceID != "" {
			_, err := awsClient.APIGW.DeleteMethod(ctx, &apigw.DeleteMethodInput{
				RestApiId:  aws.String(st.APIGatewayID),
				ResourceId: aws.String(route.ResourceID),
				HttpMethod: aws.String(route.Method),
			})
			if err != nil && !strings.Contains(err.Error(), "NotFoundException") {
				fmt.Printf("Warning: failed to delete method %s on resource %s: %v\n",
					route.Method, route.ResourceID, err)
			}
		}
	}

	// Small delay to ensure methods are fully deleted
	time.Sleep(500 * time.Millisecond)

	// 2. Delete API Gateway resources (paths)
	if err := deleteRESTAPIResources(ctx, awsClient, st.APIGatewayID, st.Resources); err != nil {
		fmt.Printf("Warning: some resources may not have been deleted: %v\n", err)
	}

	// 3. Remove Lambda permission
	statementID := fmt.Sprintf("apigateway-%s", st.APIGatewayID)
	_, _ = awsClient.Lambda.RemovePermission(ctx, &lambda.RemovePermissionInput{
		FunctionName: aws.String(st.FunctionName),
		StatementId:  aws.String(statementID),
	})

	// 4. Delete lambda
	_, _ = awsClient.Lambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(st.FunctionName),
	})

	// 5. Detach and delete role
	// Desanexa as políticas customizadas SALVAS NO ESTADO
	for _, arn := range st.AttachedPolicyARNs {
		_, _ = awsClient.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(st.RoleName),
			PolicyArn: aws.String(arn),
		})
	}

	// Desanexa a política padrão
	_, _ = awsClient.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  aws.String(st.RoleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	})

	// Deleta a Role
	_, _ = awsClient.IAM.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(st.RoleName),
	})

	// 6. Delete log group
	_, _ = awsClient.CWLogs.DeleteLogGroup(ctx, &cw.DeleteLogGroupInput{
		LogGroupName: aws.String(st.LogGroup),
	})

	d.SetId("")
	return diags
}

// Helper functions

func ensureRole(ctx context.Context, awsClient *client.AWSClient, roleName string, policyARNs []string) (string, error) {
	got, err := awsClient.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err == nil && got.Role != nil {
		// Se a role existe, garantir que as políticas estejam anexadas
		// 1. Anexa a política básica de execução de Lambda (para garantir)
		_, _ = awsClient.IAM.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
		})

		// 2. Anexa as políticas customizadas
		for _, arn := range policyARNs {
			_, err := awsClient.IAM.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
				RoleName:  aws.String(roleName),
				PolicyArn: aws.String(arn),
			})
			if err != nil {
				// Se a política já estiver anexada, ou houver um erro, apenas loga um aviso
				fmt.Printf("Warning: failed to attach policy %s to role %s: %v\n", arn, roleName, err)
			}
		}

		return aws.ToString(got.Role.Arn), nil
	}

	assume := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

	cr, cerr := awsClient.IAM.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assume),
	})
	if cerr != nil {
		if strings.Contains(cerr.Error(), "EntityAlreadyExists") {
			g2, _ := awsClient.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
			if g2 != nil && g2.Role != nil {
				return aws.ToString(g2.Role.Arn), nil
			}
		}
		return "", cerr
	}

	// 1. Anexa a política básica de execução de Lambda
	_, _ = awsClient.IAM.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	})

	// 2. Anexa as políticas customizadas
	for _, arn := range policyARNs {
		_, err := awsClient.IAM.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(arn),
		})
		if err != nil {
			fmt.Printf("Warning: failed to attach policy %s to role %s: %v\n", arn, roleName, err)
		}
	}

	time.Sleep(2 * time.Second)

	if cr != nil && cr.Role != nil {
		return aws.ToString(cr.Role.Arn), nil
	}
	return "", fmt.Errorf("role created but arn not available")
}

func ensureLambdaFunction(ctx context.Context, awsClient *client.AWSClient, fnName, roleArn, handler, runtime, zipPath string, memorySize, timeout int32, environmentVariables map[string]string) (string, error) {
	// 1. Tenta buscar a função. Se ela existir, faz o UPDATE.
	got, err := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})

	if err == nil && got.Configuration != nil {
		// A função existe. Realiza a atualização da configuração e do código.

		// Atualiza a Configuração (Role, Handler, Runtime, Memory, Timeout, Environment)
		_, uerr := awsClient.Lambda.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(fnName),
			Role:         aws.String(roleArn),
			Handler:      aws.String(handler),
			Runtime:      lambdatypes.Runtime(runtime),
			MemorySize:   aws.Int32(memorySize),
			Timeout:      aws.Int32(timeout),
			Environment: &lambdatypes.Environment{
				Variables: environmentVariables,
			},
		})
		if uerr != nil {
			return "", fmt.Errorf("failed to update lambda configuration: %w", uerr)
		}

		// Atualiza o Código
		bs, rerr := os.ReadFile(zipPath)
		if rerr != nil {
			return "", fmt.Errorf("reading zip file for update: %w", rerr)
		}

		_, upCodeErr := awsClient.Lambda.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: aws.String(fnName),
			ZipFile:      bs,
		})
		if upCodeErr != nil {
			return "", fmt.Errorf("failed to update lambda code: %w", upCodeErr)
		}

		// Aguarda a função ficar ativa após a atualização
		waiter := lambda.NewFunctionActiveWaiter(awsClient.Lambda)

		// CORREÇÃO do ERRO DE TIPO: Usar GetFunctionConfigurationInput no Waiter.Wait
		// Essa é a correção baseada na mensagem de erro do seu compilador.
		waiterErr := waiter.Wait(ctx, &lambda.GetFunctionConfigurationInput{FunctionName: aws.String(fnName)}, 30*time.Second)

		if waiterErr != nil {
			// Em caso de falha do Waiter (timeout ou erro), tenta uma checagem final.
			if _, checkErr := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)}); checkErr != nil {
				// Se o GetFunction falhar, retorna o erro.
				return "", fmt.Errorf("function update wait failed and final check failed: %w", checkErr)
			}
			// Se o GetFunction funcionar, apenas loga o warning.
			fmt.Printf("Warning: function update wait failed: %v\n", waiterErr)
		}

		return aws.ToString(got.Configuration.FunctionArn), nil
	}

	// 2. A função não existe. Faz o CREATE.
	bs, rerr := os.ReadFile(zipPath)
	if rerr != nil {
		return "", fmt.Errorf("reading zip file: %w", rerr)
	}

	// Define o Runtime
	var rt lambdatypes.Runtime
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "provided.al2", "providedal2":
		rt = lambdatypes.RuntimeProvidedal2
	case "provided.al2023", "providedal2023":
		rt = lambdatypes.RuntimeProvidedal2023
	case "python3.12":
		rt = lambdatypes.RuntimePython312
	case "nodejs20.x":
		rt = lambdatypes.RuntimeNodejs20x
	default:
		rt = lambdatypes.Runtime(runtime)
	}

	input := &lambda.CreateFunctionInput{
		FunctionName: aws.String(fnName),
		Role:         aws.String(roleArn),
		Handler:      aws.String(handler),
		Runtime:      rt,
		Code:         &lambdatypes.FunctionCode{ZipFile: bs},
		MemorySize:   aws.Int32(memorySize),
		Timeout:      aws.Int32(timeout),
		// Incluir variáveis de ambiente na criação
		Environment: &lambdatypes.Environment{
			Variables: environmentVariables,
		},
	}

	result, cerr := awsClient.Lambda.CreateFunction(ctx, input)
	if cerr != nil {
		if strings.Contains(cerr.Error(), "ResourceConflictException") {
			got, _ := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})
			if got != nil && got.Configuration != nil {
				return aws.ToString(got.Configuration.FunctionArn), nil
			}
		}
		return "", cerr
	}

	if result != nil && result.FunctionArn != nil {
		return aws.ToString(result.FunctionArn), nil
	}
	return "", fmt.Errorf("lambda created but arn not available")
}

func getRootResourceID(ctx context.Context, awsClient *client.AWSClient, apiID string) (string, error) {
	result, err := awsClient.APIGW.GetResources(ctx, &apigw.GetResourcesInput{
		RestApiId: aws.String(apiID),
	})
	if err != nil {
		return "", err
	}

	for _, res := range result.Items {
		if aws.ToString(res.Path) == "/" {
			return aws.ToString(res.Id), nil
		}
	}
	return "", fmt.Errorf("root resource not found")
}

func ensureRESTAPIPath(ctx context.Context, awsClient *client.AWSClient, apiID, rootID, path string) (string, map[string]resourceInfo, error) {
	path = strings.Trim(path, "/")
	resources := make(map[string]resourceInfo)

	if path == "" {
		return rootID, resources, nil
	}

	parts := strings.Split(path, "/")
	currentParentID := rootID
	currentPath := ""

	for _, part := range parts {
		currentPath = currentPath + "/" + part

		// Check if resource exists
		existing, err := findResourceByPath(ctx, awsClient, apiID, currentPath)
		if err == nil && existing != "" {
			currentParentID = existing
			resources[currentPath] = resourceInfo{ResourceID: existing, PathPart: part}
			continue
		}

		// Create resource
		result, err := awsClient.APIGW.CreateResource(ctx, &apigw.CreateResourceInput{
			RestApiId: aws.String(apiID),
			ParentId:  aws.String(currentParentID),
			PathPart:  aws.String(part),
		})
		if err != nil {
			if strings.Contains(err.Error(), "ConflictException") {
				// Resource exists, find it
				existing, _ := findResourceByPath(ctx, awsClient, apiID, currentPath)
				if existing != "" {
					currentParentID = existing
					resources[currentPath] = resourceInfo{ResourceID: existing, PathPart: part}
					continue
				}
			}
			return "", nil, err
		}

		currentParentID = aws.ToString(result.Id)
		resources[currentPath] = resourceInfo{ResourceID: currentParentID, PathPart: part}
	}

	return currentParentID, resources, nil
}

func findResourceByPath(ctx context.Context, awsClient *client.AWSClient, apiID, path string) (string, error) {
	result, err := awsClient.APIGW.GetResources(ctx, &apigw.GetResourcesInput{
		RestApiId: aws.String(apiID),
	})
	if err != nil {
		return "", err
	}

	for _, res := range result.Items {
		if aws.ToString(res.Path) == path {
			return aws.ToString(res.Id), nil
		}
	}
	return "", fmt.Errorf("resource not found")
}

func createRESTAPIMethod(ctx context.Context, awsClient *client.AWSClient, apiID, resourceID, httpMethod, authorization, authorizerID string) error {
	input := &apigw.PutMethodInput{
		RestApiId:         aws.String(apiID),
		ResourceId:        aws.String(resourceID),
		HttpMethod:        aws.String(httpMethod),
		AuthorizationType: aws.String(authorization),
		ApiKeyRequired:    false,
	}

	if authorization != "NONE" && authorizerID != "" && authorizerID != "<nil>" {
		input.AuthorizerId = aws.String(authorizerID)
	}

	_, err := awsClient.APIGW.PutMethod(ctx, input)
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return err
	}
	return nil
}

func createRESTAPIIntegration(ctx context.Context, awsClient *client.AWSClient, apiID, resourceID, httpMethod, functionArn, region string) error {
	uri := fmt.Sprintf("arn:aws:apigateway:%s:lambda:path/2015-03-31/functions/%s/invocations", region, functionArn)

	_, err := awsClient.APIGW.PutIntegration(ctx, &apigw.PutIntegrationInput{
		RestApiId:             aws.String(apiID),
		ResourceId:            aws.String(resourceID),
		HttpMethod:            aws.String(httpMethod),
		Type:                  "AWS_PROXY",
		IntegrationHttpMethod: aws.String("POST"),
		Uri:                   aws.String(uri),
	})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return err
	}
	return nil
}

func createMethodResponse(ctx context.Context, awsClient *client.AWSClient, apiID, resourceID, httpMethod string) error {
	_, err := awsClient.APIGW.PutMethodResponse(ctx, &apigw.PutMethodResponseInput{
		RestApiId:  aws.String(apiID),
		ResourceId: aws.String(resourceID),
		HttpMethod: aws.String(httpMethod),
		StatusCode: aws.String("200"),
	})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return err
	}
	return nil
}

func createIntegrationResponse(ctx context.Context, awsClient *client.AWSClient, apiID, resourceID, httpMethod string) error {
	_, err := awsClient.APIGW.PutIntegrationResponse(ctx, &apigw.PutIntegrationResponseInput{
		RestApiId:  aws.String(apiID),
		ResourceId: aws.String(resourceID),
		HttpMethod: aws.String(httpMethod),
		StatusCode: aws.String("200"),
	})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return err
	}
	return nil
}

func deployRESTAPI(ctx context.Context, awsClient *client.AWSClient, apiID, stageName string) error {
	_, err := awsClient.APIGW.CreateDeployment(ctx, &apigw.CreateDeploymentInput{
		RestApiId: aws.String(apiID),
		StageName: aws.String(stageName),
	})
	return err
}

func addLambdaPermission(ctx context.Context, awsClient *client.AWSClient, functionName, apiID, sourceArn string) error {
	statementID := fmt.Sprintf("apigateway-%s", apiID)

	_, err := awsClient.Lambda.AddPermission(ctx, &lambda.AddPermissionInput{
		FunctionName: aws.String(functionName),
		StatementId:  aws.String(statementID),
		Action:       aws.String("lambda:InvokeFunction"),
		Principal:    aws.String("apigateway.amazonaws.com"),
		SourceArn:    aws.String(sourceArn),
	})

	if err != nil && !strings.Contains(err.Error(), "ResourceConflictException") {
		return err
	}
	return nil
}

func getAccountID(ctx context.Context, awsClient *client.AWSClient) (string, error) {
	result, err := awsClient.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return aws.ToString(result.Account), nil
}

func extractAPIID(apiID string) string {
	parts := strings.Split(apiID, ":")
	if len(parts) > 1 {
		return parts[1]
	}
	return apiID
}

// deleteRESTAPIResources deletes API Gateway resources in the correct order
// Resources must be deleted from deepest to shallowest (children before parents)
func deleteRESTAPIResources(ctx context.Context, awsClient *client.AWSClient, apiID string, resources map[string]resourceInfo) error {
	if len(resources) == 0 {
		return nil
	}

	// Build a map of resource ID to path for sorting
	resourcePaths := make(map[string]string)
	for path, res := range resources {
		resourcePaths[res.ResourceID] = path
	}

	// Create a list of resources sorted by depth (deepest first)
	type resourceDepth struct {
		resourceID string
		path       string
		depth      int
	}

	var sortedResources []resourceDepth
	for resID, path := range resourcePaths {
		depth := strings.Count(path, "/")
		sortedResources = append(sortedResources, resourceDepth{
			resourceID: resID,
			path:       path,
			depth:      depth,
		})
	}

	// Sort by depth (descending) - deepest paths first
	for i := 0; i < len(sortedResources); i++ {
		for j := i + 1; j < len(sortedResources); j++ {
			if sortedResources[j].depth > sortedResources[i].depth {
				sortedResources[i], sortedResources[j] = sortedResources[j], sortedResources[i]
			}
		}
	}

	// Delete resources in order
	for _, res := range sortedResources {
		_, err := awsClient.APIGW.DeleteResource(ctx, &apigw.DeleteResourceInput{
			RestApiId:  aws.String(apiID),
			ResourceId: aws.String(res.resourceID),
		})
		if err != nil {
			// Log but don't fail - resource might have been deleted already
			if !strings.Contains(err.Error(), "NotFoundException") {
				fmt.Printf("Warning: failed to delete resource %s (%s): %v\n", res.path, res.resourceID, err)
			}
		}
		// Small delay to avoid rate limiting
		time.Sleep(200 * time.Millisecond)
	}

	return nil
}
