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
	apigw "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	iam "github.com/aws/aws-sdk-go-v2/service/iam"
	lambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/raywall/terraform-provider-raysouz/internal/raysouz/client"
)

// ResourceAPIGatewayLambdaRoutes returns the Terraform resource schema.
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

// resourceState structure stored in "internal"
type resourceState struct {
	RoleName      string       `json:"role_name"`
	FunctionName  string       `json:"function_name"`
	FunctionArn   string       `json:"function_arn"`
	APIGatewayID  string       `json:"api_gateway_id"`
	StageName     string       `json:"stage_name"`
	Routes        []routeState `json:"routes"`
	LogGroup      string       `json:"log_group"`
	IntegrationID string       `json:"integration_id"`
}

type routeState struct {
	Path          string `json:"path"`
	Method        string `json:"method"`
	Authorization string `json:"authorization"`
	AuthorizerID  string `json:"authorizer_id"`
	RouteID       string `json:"route_id"`
}

// resourceCreate implements creation of role, lambda, loggroup, API Gateway routes and integration.
func resourceCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	awsClient, ok := m.(*client.AWSClient)
	if !ok || awsClient == nil {
		return diag.FromErr(fmt.Errorf("aws client not configured"))
	}

	apiID := d.Get("api_gateway_id").(string)
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

	// 1) Ensure role exists (idempotent)
	roleName := fmt.Sprintf("%s-execution-role", fnName)
	roleArn, err := ensureRole(ctx, awsClient, roleName)
	if err != nil {
		return diag.FromErr(fmt.Errorf("ensure role: %w", err))
	}

	// set partial ID early so Terraform doesn't try to recreate everything on retry
	d.SetId(fmt.Sprintf("%s/%s", apiID, fnName))

	// 2) Ensure Lambda exists (create or update)
	functionArn, err := ensureLambdaFunction(ctx, awsClient, fnName, roleArn, handler, runtime, zipPath, mem, timeout)
	if err != nil {
		return diag.FromErr(fmt.Errorf("ensure lambda: %w", err))
	}

	// 3) Ensure log group & retention
	logGroup := fmt.Sprintf("/aws/lambda/%s", fnName)
	if err := awsClient.CreateLogGroupIfNotExists(ctx, logGroup, 14); err != nil {
		return diag.FromErr(fmt.Errorf("log group setup failed: %w", err))
	}

	// 4) Create API Gateway Integration
	integrationID, err := createAPIGatewayIntegration(ctx, awsClient, apiID, functionArn)
	if err != nil {
		return diag.FromErr(fmt.Errorf("create integration: %w", err))
	}

	// 5) Add Lambda permission for API Gateway to invoke
	region := awsClient.Config.Region
	accountID, err := getAccountID(ctx, awsClient)
	if err != nil {
		return diag.FromErr(fmt.Errorf("get account id: %w", err))
	}

	sourceArn := fmt.Sprintf("arn:aws:execute-api:%s:%s:%s/*/*/*", region, accountID, apiID)
	if err := addLambdaPermission(ctx, awsClient, fnName, apiID, sourceArn); err != nil {
		return diag.FromErr(fmt.Errorf("add lambda permission: %w", err))
	}

	// 6) Create routes and link to integration
	routesRaw := d.Get("routes").([]interface{})
	routes := make([]routeState, 0, len(routesRaw))

	for _, r := range routesRaw {
		rm := r.(map[string]interface{})
		path := rm["path"].(string)
		method := rm["method"].(string)
		authorization := rm["authorization"].(string)
		authorizerID := fmt.Sprintf("%v", rm["authorizer_id"])

		routeID, err := createAPIGatewayRoute(ctx, awsClient, apiID, path, method, integrationID, authorization, authorizerID)
		if err != nil {
			return diag.FromErr(fmt.Errorf("create route %s %s: %w", method, path, err))
		}

		routes = append(routes, routeState{
			Path:          path,
			Method:        method,
			Authorization: authorization,
			AuthorizerID:  authorizerID,
			RouteID:       routeID,
		})
	}

	// 7) Deploy the API to the stage
	if err := deployAPIGateway(ctx, awsClient, apiID, stage); err != nil {
		return diag.FromErr(fmt.Errorf("deploy api gateway: %w", err))
	}

	// 8) Store internal state
	st := resourceState{
		RoleName:      roleName,
		FunctionName:  fnName,
		FunctionArn:   functionArn,
		APIGatewayID:  apiID,
		StageName:     stage,
		Routes:        routes,
		LogGroup:      logGroup,
		IntegrationID: integrationID,
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

	id := d.Id()
	if id == "" {
		return diags
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

	// verify role exists
	if _, err := awsClient.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(st.RoleName)}); err != nil {
		d.SetId("")
		return diags
	}

	// verify lambda exists
	if _, err := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(st.FunctionName)}); err != nil {
		d.SetId("")
		return diags
	}

	// verify integration exists
	if st.IntegrationID != "" {
		if _, err := awsClient.APIGW.GetIntegration(ctx, &apigw.GetIntegrationInput{
			ApiId:         aws.String(st.APIGatewayID),
			IntegrationId: aws.String(st.IntegrationID),
		}); err != nil {
			d.SetId("")
			return diags
		}
	}

	return diags
}

func resourceUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	awsClient, ok := m.(*client.AWSClient)
	if !ok || awsClient == nil {
		return diag.FromErr(fmt.Errorf("aws client not configured"))
	}

	// If routes changed, delete old routes and recreate
	if d.HasChange("routes") {
		internal := d.Get("internal").(string)
		if internal != "" {
			var st resourceState
			if err := json.Unmarshal([]byte(internal), &st); err == nil {
				// Delete old routes
				for _, route := range st.Routes {
					if route.RouteID != "" {
						_, _ = awsClient.APIGW.DeleteRoute(ctx, &apigw.DeleteRouteInput{
							ApiId:   aws.String(st.APIGatewayID),
							RouteId: aws.String(route.RouteID),
						})
					}
				}
			}
		}
	}

	// Recreate with new configuration
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

	// Delete routes
	for _, route := range st.Routes {
		if route.RouteID != "" {
			_, _ = awsClient.APIGW.DeleteRoute(ctx, &apigw.DeleteRouteInput{
				ApiId:   aws.String(st.APIGatewayID),
				RouteId: aws.String(route.RouteID),
			})
		}
	}

	// Delete integration
	if st.IntegrationID != "" {
		_, _ = awsClient.APIGW.DeleteIntegration(ctx, &apigw.DeleteIntegrationInput{
			ApiId:         aws.String(st.APIGatewayID),
			IntegrationId: aws.String(st.IntegrationID),
		})
	}

	// Remove Lambda permission
	statementID := fmt.Sprintf("apigateway-%s", st.APIGatewayID)
	_, _ = awsClient.Lambda.RemovePermission(ctx, &lambda.RemovePermissionInput{
		FunctionName: aws.String(st.FunctionName),
		StatementId:  aws.String(statementID),
	})

	// Delete lambda
	_, _ = awsClient.Lambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(st.FunctionName),
	})

	// Detach and delete role
	_, _ = awsClient.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  aws.String(st.RoleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	})
	_, _ = awsClient.IAM.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(st.RoleName),
	})

	// Delete log group
	_, _ = awsClient.CWLogs.DeleteLogGroup(ctx, &cw.DeleteLogGroupInput{
		LogGroupName: aws.String(st.LogGroup),
	})

	d.SetId("")
	return diags
}

// Helper functions

func ensureRole(ctx context.Context, awsClient *client.AWSClient, roleName string) (string, error) {
	iamClient := awsClient.IAM

	got, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err == nil && got.Role != nil {
		return aws.ToString(got.Role.Arn), nil
	}

	assume := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

	cr, cerr := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assume),
	})
	if cerr != nil {
		if strings.Contains(cerr.Error(), "EntityAlreadyExists") || strings.Contains(cerr.Error(), "RoleAlreadyExists") {
			g2, g2err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
			if g2err != nil {
				return "", fmt.Errorf("role exists but cannot be read: %w", g2err)
			}
			return aws.ToString(g2.Role.Arn), nil
		}
		return "", fmt.Errorf("create role: %w", cerr)
	}

	_, _ = iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	})

	for i := 0; i < 6; i++ {
		g, gerr := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
		if gerr == nil && g.Role != nil {
			return aws.ToString(g.Role.Arn), nil
		}
		time.Sleep(400 * time.Millisecond)
	}

	if cr != nil && cr.Role != nil && cr.Role.Arn != nil {
		return aws.ToString(cr.Role.Arn), nil
	}
	return "", fmt.Errorf("role created but not available yet")
}

func ensureLambdaFunction(ctx context.Context, awsClient *client.AWSClient, fnName, roleArn, handler, runtime, zipPath string, memorySize, timeout int32) (string, error) {
	lmb := awsClient.Lambda

	got, err := lmb.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})
	if err == nil && got.Configuration != nil {
		return aws.ToString(got.Configuration.FunctionArn), nil
	}

	bs, rerr := os.ReadFile(zipPath)
	if rerr != nil {
		return "", fmt.Errorf("reading zip file: %w", rerr)
	}

	var rt lambdatypes.Runtime
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "provided.al2", "providedal2":
		rt = lambdatypes.RuntimeProvidedal2
	case "provided.al2023", "providedal2023":
		rt = lambdatypes.RuntimeProvidedal2023
	case "python3.12":
		rt = lambdatypes.RuntimePython312
	case "python3.11":
		rt = lambdatypes.RuntimePython311
	case "nodejs20.x":
		rt = lambdatypes.RuntimeNodejs20x
	case "nodejs18.x":
		rt = lambdatypes.RuntimeNodejs18x
	default:
		rt = lambdatypes.Runtime(runtime)
	}

	createInput := &lambda.CreateFunctionInput{
		FunctionName: aws.String(fnName),
		Role:         aws.String(roleArn),
		Handler:      aws.String(handler),
		Runtime:      rt,
		Code: &lambdatypes.FunctionCode{
			ZipFile: bs,
		},
		MemorySize: aws.Int32(memorySize),
		Timeout:    aws.Int32(timeout),
	}

	result, cerr := lmb.CreateFunction(ctx, createInput)
	if cerr != nil {
		if strings.Contains(cerr.Error(), "ResourceConflictException") ||
			strings.Contains(cerr.Error(), "Function already exist") ||
			strings.Contains(cerr.Error(), "ResourceAlreadyExistsException") {
			got, _ := lmb.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})
			if got != nil && got.Configuration != nil {
				return aws.ToString(got.Configuration.FunctionArn), nil
			}
			return "", fmt.Errorf("function exists but arn not available")
		}
		return "", fmt.Errorf("create lambda: %w", cerr)
	}

	for i := 0; i < 8; i++ {
		g, gerr := lmb.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})
		if gerr == nil && g.Configuration != nil {
			return aws.ToString(g.Configuration.FunctionArn), nil
		}
		time.Sleep(400 * time.Millisecond)
	}

	if result != nil && result.FunctionArn != nil {
		return aws.ToString(result.FunctionArn), nil
	}
	return "", fmt.Errorf("lambda created but arn not available")
}

func createAPIGatewayIntegration(ctx context.Context, awsClient *client.AWSClient, apiID, functionArn string) (string, error) {
	result, err := awsClient.APIGW.CreateIntegration(ctx, &apigw.CreateIntegrationInput{
		ApiId:                aws.String(apiID),
		IntegrationType:      apigwtypes.IntegrationTypeAwsProxy,
		IntegrationUri:       aws.String(functionArn),
		PayloadFormatVersion: aws.String("2.0"),
	})
	if err != nil {
		return "", fmt.Errorf("create integration: %w", err)
	}
	return aws.ToString(result.IntegrationId), nil
}

func createAPIGatewayRoute(ctx context.Context, awsClient *client.AWSClient, apiID, path, method, integrationID, authorization, authorizerID string) (string, error) {
	routeKey := fmt.Sprintf("%s %s", strings.ToUpper(method), path)

	input := &apigw.CreateRouteInput{
		ApiId:    aws.String(apiID),
		RouteKey: aws.String(routeKey),
		Target:   aws.String(fmt.Sprintf("integrations/%s", integrationID)),
	}

	if strings.ToUpper(authorization) != "NONE" && authorizerID != "" && authorizerID != "<nil>" {
		input.AuthorizationType = apigwtypes.AuthorizationType(authorization)
		input.AuthorizerId = aws.String(authorizerID)
	}

	result, err := awsClient.APIGW.CreateRoute(ctx, input)
	if err != nil {
		return "", fmt.Errorf("create route: %w", err)
	}
	return aws.ToString(result.RouteId), nil
}

func deployAPIGateway(ctx context.Context, awsClient *client.AWSClient, apiID, stageName string) error {
	_, err := awsClient.APIGW.CreateDeployment(ctx, &apigw.CreateDeploymentInput{
		ApiId:     aws.String(apiID),
		StageName: aws.String(stageName),
	})
	if err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	return nil
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
		return fmt.Errorf("add permission: %w", err)
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
