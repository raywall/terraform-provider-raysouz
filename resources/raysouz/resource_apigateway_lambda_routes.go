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
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	iam "github.com/aws/aws-sdk-go-v2/service/iam"
	lambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

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
	RoleName     string              `json:"role_name"`
	FunctionName string              `json:"function_name"`
	APIGatewayID string              `json:"api_gateway_id"`
	StageName    string              `json:"stage_name"`
	Routes       []map[string]string `json:"routes"`
	LogGroup     string              `json:"log_group"`
}

// resourceCreate implements creation of role, lambda, loggroup and stores state.
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
	if err := ensureLambdaFunction(ctx, awsClient, fnName, roleArn, handler, runtime, zipPath, mem, timeout); err != nil {
		return diag.FromErr(fmt.Errorf("ensure lambda: %w", err))
	}

	// 3) Ensure log group & retention
	logGroup := fmt.Sprintf("/aws/lambda/%s", fnName)
	if err := awsClient.CreateLogGroupIfNotExists(ctx, logGroup, 14); err != nil {
		// surface error (role/function already created), do not attempt destructive rollback
		return diag.FromErr(fmt.Errorf("log group setup failed: %w", err))
	}

	// 4) Collect routes (we do not yet create API Gateway resources here — TODO)
	routesRaw := d.Get("routes").([]interface{})
	routes := make([]map[string]string, 0, len(routesRaw))
	for _, r := range routesRaw {
		rm := r.(map[string]interface{})
		routes = append(routes, map[string]string{
			"path":          rm["path"].(string),
			"method":        rm["method"].(string),
			"authorization": rm["authorization"].(string),
			"authorizer_id": fmt.Sprintf("%v", rm["authorizer_id"]),
		})
	}

	// 5) Store internal state
	st := resourceState{
		RoleName:     roleName,
		FunctionName: fnName,
		APIGatewayID: apiID,
		StageName:    stage,
		Routes:       routes,
		LogGroup:     logGroup,
	}
	b, _ := json.Marshal(st)
	_ = d.Set("internal", string(b))

	// small wait to improve eventual-consistency
	time.Sleep(400 * time.Millisecond)
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
		// nothing to read
		return diags
	}

	internal := d.Get("internal").(string)
	if internal == "" {
		// nothing stored; nothing to do (could attempt import)
		return diags
	}

	var st resourceState
	if err := json.Unmarshal([]byte(internal), &st); err != nil {
		// invalid internal -> clear state to force recreate/import
		d.SetId("")
		return diag.FromErr(fmt.Errorf("failed reading internal state: %w", err))
	}

	// verify role exists via IAM GetRole
	if _, err := awsClient.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(st.RoleName)}); err != nil {
		// assume resource deleted externally
		d.SetId("")
		return diags
	}

	// verify lambda exists
	if _, err := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(st.FunctionName)}); err != nil {
		d.SetId("")
		return diags
	}

	// TODO: verify api gateway resources/methods
	_ = cw.NewFromConfig(awsClient.Config) // keep package usage explicit if needed later

	return diags
}

func resourceUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	// For now, handle update by reusing create: idempotent ensures safe operation.
	// You can optimize to perform granular updates later.
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

	// delete lambda
	_, _ = awsClient.Lambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{FunctionName: aws.String(st.FunctionName)})

	// delete role
	// In production you should detach managed policies and delete inline policies first.
	_, _ = awsClient.IAM.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(st.RoleName)})

	// delete log group
	_, _ = awsClient.CWLogs.DeleteLogGroup(ctx, &cw.DeleteLogGroupInput{LogGroupName: aws.String(st.LogGroup)})

	d.SetId("")
	return diags
}

// Helpers

// ensureRole returns role ARN; creates role if missing.
func ensureRole(ctx context.Context, awsClient *client.AWSClient, roleName string) (string, error) {
	iamClient := awsClient.IAM

	// try get role
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
		// if already exists concurrently, attempt to get it
		if strings.Contains(cerr.Error(), "EntityAlreadyExists") || strings.Contains(cerr.Error(), "RoleAlreadyExists") {
			g2, g2err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
			if g2err != nil {
				return "", fmt.Errorf("role exists but cannot be read: %w", g2err)
			}
			return aws.ToString(g2.Role.Arn), nil
		}
		return "", fmt.Errorf("create role: %w", cerr)
	}

	// attach AWSLambdaBasicExecutionRole (managed) to allow logs
	_, _ = iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	})

	// wait until role is readable
	for i := 0; i < 6; i++ {
		g, gerr := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
		if gerr == nil && g.Role != nil {
			if cr != nil && cr.Role != nil && cr.Role.Arn != nil {
				return aws.ToString(cr.Role.Arn), nil
			}
			return aws.ToString(g.Role.Arn), nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	// best-effort return ARN if present
	if cr != nil && cr.Role != nil && cr.Role.Arn != nil {
		return aws.ToString(cr.Role.Arn), nil
	}
	return "", fmt.Errorf("role created but not available yet")
}

// ensureLambdaFunction creates the Lambda function if missing; otherwise no-op (you can add update path)
func ensureLambdaFunction(ctx context.Context, awsClient *client.AWSClient, fnName, roleArn, handler, runtime, zipPath string, memorySize, timeout int32) error {
	lmb := awsClient.Lambda

	_, err := lmb.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})
	if err == nil {
		// already exists -> no-op. You can implement update logic here.
		return nil
	}

	// read zip file bytes
	bs, rerr := os.ReadFile(zipPath)
	if rerr != nil {
		return fmt.Errorf("reading zip file: %w", rerr)
	}

	// convert runtime string to lambda runtime type safely
	var rt lambdatypes.Runtime
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "provided.al2", "providedal2":
		rt = lambdatypes.RuntimeProvidedal2
	default:
		// fallback using direct conversion (may be invalid for unknown strings)
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

	_, cerr := lmb.CreateFunction(ctx, createInput)
	if cerr != nil {
		// ignore resource conflict if created concurrently
		if strings.Contains(cerr.Error(), "ResourceConflictException") || strings.Contains(cerr.Error(), "Function already exist") || strings.Contains(cerr.Error(), "ResourceAlreadyExistsException") {
			return nil
		}
		return fmt.Errorf("create lambda: %w", cerr)
	}

	// wait until available
	for i := 0; i < 8; i++ {
		_, gerr := lmb.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(fnName)})
		if gerr == nil {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("lambda created but not available yet")
}
