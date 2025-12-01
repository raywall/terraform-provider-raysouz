package raysouz

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/raywall/terraform-provider-raysouz/internal/raysouz/client"
)

func ResourceAPIGatewayLambdaRoutes() *schema.Resource {
	return &schema.Resource{
		Description: "Creates and manages Lambda functions with API Gateway routes integration",

		CreateContext: resourceAPIGatewayLambdaRoutesCreate,
		ReadContext:   resourceAPIGatewayLambdaRoutesRead,
		UpdateContext: resourceAPIGatewayLambdaRoutesUpdate,
		DeleteContext: resourceAPIGatewayLambdaRoutesDelete,

		Schema: map[string]*schema.Schema{
			"api_gateway_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[a-z0-9-]+$`), "must be a valid API Gateway ID"),
				Description:  "ID of the existing API Gateway REST API",
			},

			"stage_name": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "prod",
				Description: "API Gateway stage name to deploy to",
			},

			"lambda_config": {
				Type:     schema.TypeList,
				Required: true,
				MaxItems: 1,
				MinItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"function_name": {
							Type:         schema.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[a-zA-Z0-9-_]{1,64}$`), "must be a valid Lambda function name (1-64 chars, alphanumeric, hyphens, underscores)"),
							Description:  "Name of the Lambda function",
						},
						"runtime": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice([]string{
								"provided.al2",
								"provided.al2023",
								"nodejs18.x",
								"nodejs20.x",
								"python3.9",
								"python3.10",
								"python3.11",
								"python3.12",
								"java11",
								"java17",
								"go1.x",
								"dotnet6",
								"dotnet8",
								"ruby3.2",
							}, false),
							Description: "Lambda runtime",
						},
						"handler": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "Lambda function handler",
						},
						"zip_file": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringMatch(regexp.MustCompile(`\.zip$`), "must be a .zip file"),
							Description:  "Path to the Lambda function ZIP file",
						},
						"memory_size": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      128,
							ValidateFunc: validation.IntBetween(128, 10240),
							Description:  "Memory allocation for Lambda function (128-10240 MB)",
						},
						"timeout": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      3,
							ValidateFunc: validation.IntBetween(1, 900),
							Description:  "Timeout for Lambda function (1-900 seconds)",
						},
						"environment_variables": {
							Type:        schema.TypeMap,
							Optional:    true,
							Elem:        &schema.Schema{Type: schema.TypeString},
							Description: "Environment variables for the Lambda function",
						},
						"layers": {
							Type:        schema.TypeList,
							Optional:    true,
							Elem:        &schema.Schema{Type: schema.TypeString},
							Description: "Lambda layer ARNs",
						},
						"vpc_config": {
							Type:        schema.TypeList,
							Optional:    true,
							MaxItems:    1,
							Description: "VPC configuration for the Lambda function",
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"subnet_ids": {
										Type:        schema.TypeList,
										Required:    true,
										MinItems:    1,
										Elem:        &schema.Schema{Type: schema.TypeString},
										Description: "List of subnet IDs",
									},
									"security_group_ids": {
										Type:        schema.TypeList,
										Required:    true,
										MinItems:    1,
										Elem:        &schema.Schema{Type: schema.TypeString},
										Description: "List of security group IDs",
									},
								},
							},
						},
					},
				},
			},

			"log_retention_days": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      30,
				ValidateFunc: validation.IntInSlice([]int{1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1827, 3653}),
				Description:  "CloudWatch Logs retention in days",
			},

			"routes": {
				Type:        schema.TypeList,
				Required:    true,
				MinItems:    1,
				Description: "API Gateway routes to create",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"path": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringMatch(regexp.MustCompile(`^(\/[a-zA-Z0-9\.\_\-\{\}\+]+)+$`), "must be a valid API path"),
							Description:  "API route path",
						},
						"method": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice([]string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "ANY"}, false),
							Description:  "HTTP method",
						},
						"authorization": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "NONE",
							ValidateFunc: validation.StringInSlice([]string{"NONE", "AWS_IAM", "CUSTOM", "COGNITO_USER_POOLS"}, false),
							Description:  "Authorization type",
						},
						"authorizer_id": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "API Gateway authorizer ID",
						},
						"request_parameters": {
							Type:        schema.TypeMap,
							Optional:    true,
							Elem:        &schema.Schema{Type: schema.TypeBool},
							Description: "Request parameters mapping",
						},
						"integration_type": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "AWS_PROXY",
							ValidateFunc: validation.StringInSlice([]string{"AWS_PROXY", "AWS", "HTTP_PROXY", "HTTP", "MOCK"}, false),
							Description:  "Integration type",
						},
					},
				},
			},

			"ssm_parameters": {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "SSM Parameters to create",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringMatch(regexp.MustCompile(`^\/[a-zA-Z0-9\.\_\-\/]+$`), "must be a valid SSM parameter path starting with /"),
							Description:  "Parameter name/path",
						},
						"type": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice([]string{"String", "StringList", "SecureString"}, false),
							Description:  "Parameter type",
						},
						"value": {
							Type:        schema.TypeString,
							Required:    true,
							Sensitive:   true,
							Description: "Parameter value",
						},
						"description": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "Parameter description",
						},
						"tier": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "Standard",
							ValidateFunc: validation.StringInSlice([]string{"Standard", "Advanced", "Intelligent-Tiering"}, false),
							Description:  "Parameter tier",
						},
						"key_id": {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "KMS Key ID for SecureString parameters",
						},
					},
				},
			},

			// Computed attributes
			"lambda_function_arn": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "ARN of the created Lambda function",
			},
			"lambda_role_arn": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "ARN of the IAM execution role",
			},
			"cloudwatch_log_group_arn": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "ARN of the CloudWatch Log Group",
			},
			"api_execution_arn": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "API Gateway execution ARN",
			},
			"created_at": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Creation timestamp",
			},
			"last_modified": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Last modification timestamp",
			},
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(30 * time.Minute),
			Update: schema.DefaultTimeout(30 * time.Minute),
			Delete: schema.DefaultTimeout(30 * time.Minute),
			Read:   schema.DefaultTimeout(5 * time.Minute),
		},

		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
	}
}

func resourceAPIGatewayLambdaRoutesCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	awsClient := meta.(*client.AWSClient)

	// Parse configuration
	apiGatewayID := d.Get("api_gateway_id").(string)
	stageName := d.Get("stage_name").(string)
	logRetentionDays := int32(d.Get("log_retention_days").(int))

	// Parse lambda config
	lambdaConfigRaw := d.Get("lambda_config").([]interface{})[0].(map[string]interface{})
	lambdaConfig := parseLambdaConfig(lambdaConfigRaw)

	// 1. Create IAM role for Lambda
	roleArn, err := awsClient.CreateLambdaExecutionRole(ctx, lambdaConfig.FunctionName)
	if err != nil {
		return diag.Errorf("creating IAM role: %s", err)
	}

	// 2. Create CloudWatch Log Group
	logGroupArn, err := awsClient.CreateCloudWatchLogGroup(ctx, lambdaConfig.FunctionName, logRetentionDays)
	if err != nil {
		return diag.Errorf("creating CloudWatch log group: %s", err)
	}

	// 3. Create Lambda function
	functionOutput, err := awsClient.CreateLambdaFunction(ctx, &lambdaConfig, roleArn)
	if err != nil {
		return diag.Errorf("creating Lambda function: %s", err)
	}
	functionArn := *functionOutput.FunctionArn

	// 4. Create SSM Parameters
	if ssmParamsRaw, ok := d.Get("ssm_parameters").([]interface{}); ok {
		for _, paramRaw := range ssmParamsRaw {
			paramMap := paramRaw.(map[string]interface{})
			param := client.SSMParameter{
				Name:        paramMap["name"].(string),
				Type:        paramMap["type"].(string),
				Value:       paramMap["value"].(string),
				Description: paramMap["description"].(string),
				Tier:        paramMap["tier"].(string),
				KeyID:       paramMap["key_id"].(string),
			}

			if err := awsClient.CreateSSMParameter(ctx, param); err != nil {
				return diag.Errorf("creating SSM parameter %s: %s", param.Name, err)
			}
		}
	}

	// 5. Create API Gateway routes and integrations
	routesRaw := d.Get("routes").([]interface{})
	apiExecutionArn := fmt.Sprintf(
		"arn:aws:execute-api:%s:%s:%s/*",
		awsClient.Region,
		awsClient.AccountID,
		apiGatewayID,
	)

	// Add permission for API Gateway to invoke Lambda
	if err := awsClient.AddLambdaPermission(ctx, lambdaConfig.FunctionName, apiGatewayID, apiExecutionArn); err != nil {
		return diag.Errorf("adding Lambda permission: %s", err)
	}

	for _, routeRaw := range routesRaw {
		routeMap := routeRaw.(map[string]interface{})
		routeConfig := parseRouteConfig(routeMap)

		if err := awsClient.CreateAPIGatewayIntegration(ctx, apiGatewayID, functionArn, routeConfig); err != nil {
			return diag.Errorf("creating API Gateway integration for route %s %s: %s", routeConfig.Method, routeConfig.Path, err)
		}
	}

	// 6. Deploy API Gateway
	if err := awsClient.DeployAPIGateway(ctx, apiGatewayID, stageName); err != nil {
		return diag.Errorf("deploying API Gateway: %s", err)
	}

	// Set ID and computed attributes
	d.SetId(fmt.Sprintf("%s/%s", apiGatewayID, lambdaConfig.FunctionName))

	if err := d.Set("lambda_function_arn", functionArn); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("lambda_role_arn", roleArn); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("cloudwatch_log_group_arn", logGroupArn); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("api_execution_arn", apiExecutionArn); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("created_at", time.Now().Format(time.RFC3339)); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("last_modified", time.Now().Format(time.RFC3339)); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceAPIGatewayLambdaRoutesRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	awsClient := meta.(*client.AWSClient)

	parts := strings.Split(d.Id(), "/")
	if len(parts) != 2 {
		return diag.Errorf("invalid ID format: %s (expected api_gateway_id/function_name)", d.Id())
	}

	apiGatewayID := parts[0]
	functionName := parts[1]

	// Check if Lambda function exists - CORREÇÃO: Usar SDK v2
	function, err := awsClient.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "ResourceNotFoundException") {
			d.SetId("")
			return nil
		}
		return diag.Errorf("reading Lambda function: %s", err)
	}

	// Check if API Gateway exists - CORREÇÃO: Usar SDK v2
	_, err = awsClient.APIGateway.GetRestApi(ctx, &apigateway.GetRestApiInput{
		RestApiId: aws.String(apiGatewayID),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NotFoundException") {
			d.SetId("")
			return nil
		}
		return diag.Errorf("reading API Gateway: %s", err)
	}

	// Set basic attributes
	if err := d.Set("api_gateway_id", apiGatewayID); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("lambda_function_arn", function.Configuration.FunctionArn); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceAPIGatewayLambdaRoutesUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	awsClient := meta.(*client.AWSClient)

	parts := strings.Split(d.Id(), "/")
	if len(parts) != 2 {
		return diag.Errorf("invalid ID format: %s", d.Id())
	}

	functionName := parts[1]

	// Handle updates to Lambda configuration
	if d.HasChange("lambda_config") {
		oldRaw, newRaw := d.GetChange("lambda_config")
		oldConfig := parseLambdaConfig(oldRaw.([]interface{})[0].(map[string]interface{}))
		newConfig := parseLambdaConfig(newRaw.([]interface{})[0].(map[string]interface{}))

		// Update Lambda function configuration - CORREÇÃO: Usar SDK v2
		updateConfigInput := &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(functionName),
		}

		if d.HasChange("lambda_config.0.memory_size") {
			updateConfigInput.MemorySize = aws.Int32(newConfig.MemorySize)
		}
		if d.HasChange("lambda_config.0.timeout") {
			updateConfigInput.Timeout = aws.Int32(newConfig.Timeout)
		}
		if d.HasChange("lambda_config.0.environment_variables") {
			envVars := make(map[string]string)
			for k, v := range newConfig.EnvironmentVariables {
				envVars[k] = v
			}
			updateConfigInput.Environment = &lambdatypes.Environment{
				Variables: envVars,
			}
		}
		if d.HasChange("lambda_config.0.layers") {
			updateConfigInput.Layers = newConfig.Layers
		}
		if d.HasChange("lambda_config.0.vpc_config") {
			if newConfig.VPCConfig != nil {
				updateConfigInput.VpcConfig = &lambdatypes.VpcConfig{
					SubnetIds:        newConfig.VPCConfig.SubnetIDs,
					SecurityGroupIds: newConfig.VPCConfig.SecurityGroupIDs,
				}
			}
		}

		_, err := awsClient.Lambda.UpdateFunctionConfiguration(ctx, updateConfigInput)
		if err != nil {
			return diag.Errorf("updating Lambda function configuration: %s", err)
		}

		// Update function code if zip file changed
		if oldConfig.ZipFilePath != newConfig.ZipFilePath {
			zipBytes, err := os.ReadFile(newConfig.ZipFilePath)
			if err != nil {
				return diag.Errorf("reading zip file: %s", err)
			}

			_, err = awsClient.Lambda.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
				FunctionName: aws.String(functionName),
				ZipFile:      zipBytes,
				Publish:      true,
			})
			if err != nil {
				return diag.Errorf("updating Lambda function code: %s", err)
			}
		}
	}

	// Update CloudWatch retention
	if d.HasChange("log_retention_days") {
		logRetentionDays := int32(d.Get("log_retention_days").(int))
		logGroupName := fmt.Sprintf("/aws/lambda/%s", functionName)

		_, err := awsClient.CloudWatch.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
			LogGroupName:    &logGroupName,
			RetentionInDays: &logRetentionDays,
		})
		if err != nil {
			return diag.Errorf("updating CloudWatch retention policy: %s", err)
		}
	}

	// Update SSM parameters
	if d.HasChange("ssm_parameters") {
		// TODO: Implement SSM parameter updates
		// This would involve comparing old and new parameters
	}

	// Update routes
	if d.HasChange("routes") {
		// TODO: Implement route updates
		// Note: This is complex as API Gateway resources can't be easily updated
		// Might need to delete and recreate routes
	}

	// Re-deploy API Gateway if routes changed
	if d.HasChanges("routes", "stage_name") {
		apiGatewayID := parts[0]
		stageName := d.Get("stage_name").(string)
		if err := awsClient.DeployAPIGateway(ctx, apiGatewayID, stageName); err != nil {
			return diag.Errorf("redeploying API Gateway: %s", err)
		}
	}

	if err := d.Set("last_modified", time.Now().Format(time.RFC3339)); err != nil {
		return diag.FromErr(err)
	}

	return resourceAPIGatewayLambdaRoutesRead(ctx, d, meta)
}

func resourceAPIGatewayLambdaRoutesDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	awsClient := meta.(*client.AWSClient)

	parts := strings.Split(d.Id(), "/")
	if len(parts) != 2 {
		return diag.Errorf("invalid ID format: %s", d.Id())
	}

	apiGatewayID := parts[0]
	functionName := parts[1]

	// 1. Remove Lambda permission for API Gateway
	if err := awsClient.RemoveLambdaPermission(ctx, functionName, apiGatewayID); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return diag.Errorf("removing Lambda permission: %s", err)
		}
	}

	// 2. Delete Lambda function
	if err := awsClient.DeleteLambdaFunction(ctx, functionName); err != nil {
		return diag.Errorf("deleting Lambda function: %s", err)
	}

	// 3. Delete IAM role
	if err := awsClient.DeleteLambdaExecutionRole(ctx, functionName); err != nil {
		return diag.Errorf("deleting IAM role: %s", err)
	}

	// 4. Delete CloudWatch Log Group (optional)
	if err := awsClient.DeleteCloudWatchLogGroup(ctx, functionName); err != nil {
		// Log warning but don't fail
	}

	// 5. Delete SSM Parameters
	if ssmParamsRaw, ok := d.Get("ssm_parameters").([]interface{}); ok {
		for _, paramRaw := range ssmParamsRaw {
			paramMap := paramRaw.(map[string]interface{})
			paramName := paramMap["name"].(string)

			if err := awsClient.DeleteSSMParameter(ctx, paramName); err != nil {
				return diag.Errorf("deleting SSM parameter %s: %s", paramName, err)
			}
		}
	}

	// Note: We don't delete API Gateway routes as they might be used by other functions
	// This is a design decision

	d.SetId("")
	return nil
}

// Helper functions
func parseLambdaConfig(raw map[string]interface{}) client.LambdaConfig {
	config := client.LambdaConfig{
		FunctionName: raw["function_name"].(string),
		Runtime:      raw["runtime"].(string),
		Handler:      raw["handler"].(string),
		ZipFilePath:  raw["zip_file"].(string),
		MemorySize:   int32(raw["memory_size"].(int)),
		Timeout:      int32(raw["timeout"].(int)),
	}

	// Parse environment variables
	if envVarsRaw, ok := raw["environment_variables"].(map[string]interface{}); ok {
		config.EnvironmentVariables = make(map[string]string)
		for k, v := range envVarsRaw {
			config.EnvironmentVariables[k] = v.(string)
		}
	}

	// Parse layers
	if layersRaw, ok := raw["layers"].([]interface{}); ok {
		config.Layers = make([]string, len(layersRaw))
		for i, layer := range layersRaw {
			config.Layers[i] = layer.(string)
		}
	}

	// Parse VPC config
	if vpcConfigRaw, ok := raw["vpc_config"].([]interface{}); ok && len(vpcConfigRaw) > 0 {
		vpcMap := vpcConfigRaw[0].(map[string]interface{})
		vpcConfig := &client.VPCConfig{}

		if subnetsRaw, ok := vpcMap["subnet_ids"].([]interface{}); ok {
			vpcConfig.SubnetIDs = make([]string, len(subnetsRaw))
			for i, subnet := range subnetsRaw {
				vpcConfig.SubnetIDs[i] = subnet.(string)
			}
		}

		if sgsRaw, ok := vpcMap["security_group_ids"].([]interface{}); ok {
			vpcConfig.SecurityGroupIDs = make([]string, len(sgsRaw))
			for i, sg := range sgsRaw {
				vpcConfig.SecurityGroupIDs[i] = sg.(string)
			}
		}

		config.VPCConfig = vpcConfig
	}

	return config
}

func parseRouteConfig(raw map[string]interface{}) client.RouteConfig {
	config := client.RouteConfig{
		Path:            raw["path"].(string),
		Method:          raw["method"].(string),
		Authorization:   raw["authorization"].(string),
		IntegrationType: raw["integration_type"].(string),
	}

	if authorizerID, ok := raw["authorizer_id"].(string); ok && authorizerID != "" {
		config.AuthorizerID = authorizerID
	}

	if reqParamsRaw, ok := raw["request_parameters"].(map[string]interface{}); ok {
		config.RequestParameters = make(map[string]bool)
		for k, v := range reqParamsRaw {
			config.RequestParameters[k] = v.(bool)
		}
	}

	return config
}
