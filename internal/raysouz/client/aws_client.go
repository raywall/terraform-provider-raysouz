package client

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	apigatewaytypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

type AWSClient struct {
	cfg        aws.Config
	APIGateway *apigateway.Client
	Lambda     *lambda.Client
	IAM        *iam.Client
	CloudWatch *cloudwatchlogs.Client
	SSM        *ssm.Client
	Region     string
	AccountID  string
}

type LambdaConfig struct {
	FunctionName         string
	Runtime              string
	Handler              string
	ZipFilePath          string
	MemorySize           int32
	Timeout              int32
	EnvironmentVariables map[string]string
	Layers               []string
	VPCConfig            *VPCConfig
}

type VPCConfig struct {
	SubnetIDs        []string
	SecurityGroupIDs []string
}

type RouteConfig struct {
	Path              string
	Method            string
	Authorization     string
	AuthorizerID      string
	RequestParameters map[string]bool
	IntegrationType   string
}

type SSMParameter struct {
	Name        string
	Type        string
	Value       string
	Description string
	Tier        string
	KeyID       string
}

func NewAWSClient(ctx context.Context, region, accessKey, secretKey string) (*AWSClient, error) {
	var cfg aws.Config
	var err error

	// Load configuration
	if accessKey != "" && secretKey != "" {
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		)
	} else {
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	// Get account ID
	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("getting caller identity: %w", err)
	}

	client := &AWSClient{
		cfg:        cfg,
		APIGateway: apigateway.NewFromConfig(cfg),
		Lambda:     lambda.NewFromConfig(cfg),
		IAM:        iam.NewFromConfig(cfg),
		CloudWatch: cloudwatchlogs.NewFromConfig(cfg),
		SSM:        ssm.NewFromConfig(cfg),
		Region:     region,
		AccountID:  aws.ToString(identity.Account),
	}

	return client, nil
}

// IAM Role Management
func (c *AWSClient) CreateLambdaExecutionRole(ctx context.Context, functionName string) (string, error) {
	roleName := fmt.Sprintf("%s-execution-role", functionName)
	assumeRolePolicy := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"Service": "lambda.amazonaws.com"
				},
				"Action": "sts:AssumeRole"
			}
		]
	}`

	// Create role
	createRoleOutput, err := c.IAM.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicy),
		Description:              aws.String(fmt.Sprintf("Execution role for Lambda function %s", functionName)),
		Tags: []iamtypes.Tag{
			{
				Key:   aws.String("ManagedBy"),
				Value: aws.String("terraform-provider-raysouz"),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating IAM role: %w", err)
	}

	// Attach basic execution policy
	_, err = c.IAM.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"),
	})
	if err != nil {
		return "", fmt.Errorf("attaching basic execution policy: %w", err)
	}

	return aws.ToString(createRoleOutput.Role.Arn), nil
}

func (c *AWSClient) DeleteLambdaExecutionRole(ctx context.Context, functionName string) error {
	roleName := fmt.Sprintf("%s-execution-role", functionName)

	// Detach policies
	policies := []string{
		"arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole",
		"arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole",
	}

	for _, policyArn := range policies {
		_, err := c.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyArn),
		})
		if err != nil {
			if !strings.Contains(err.Error(), "NoSuchEntity") {
				return fmt.Errorf("detaching policy %s: %w", policyArn, err)
			}
		}
	}

	// Delete role
	_, err := c.IAM.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "NoSuchEntity") {
			return fmt.Errorf("deleting IAM role: %w", err)
		}
	}

	return nil
}

// Lambda Function Management
func (c *AWSClient) CreateLambdaFunction(ctx context.Context, config *LambdaConfig, roleArn string) (*lambda.CreateFunctionOutput, error) {
	// Read zip file
	zipBytes, err := os.ReadFile(config.ZipFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading zip file: %w", err)
	}

	createInput := &lambda.CreateFunctionInput{
		FunctionName: aws.String(config.FunctionName),
		Runtime:      lambdatypes.Runtime(config.Runtime),
		Role:         aws.String(roleArn),
		Handler:      aws.String(config.Handler),
		Code: &lambdatypes.FunctionCode{
			ZipFile: zipBytes,
		},
		Description: aws.String("Managed by Terraform provider raysouz"),
		MemorySize:  aws.Int32(config.MemorySize),
		Timeout:     aws.Int32(config.Timeout),
		Publish:     true,
		Tags: map[string]string{
			"ManagedBy": "terraform-provider-raysouz",
		},
	}

	// Add environment variables
	if config.EnvironmentVariables != nil {
		envVars := make(map[string]string)
		for k, v := range config.EnvironmentVariables {
			envVars[k] = v
		}
		createInput.Environment = &lambdatypes.Environment{
			Variables: envVars,
		}
	}

	// Add layers
	if len(config.Layers) > 0 {
		createInput.Layers = config.Layers
	}

	// Add VPC config
	if config.VPCConfig != nil {
		createInput.VpcConfig = &lambdatypes.VpcConfig{
			SubnetIds:        config.VPCConfig.SubnetIDs,
			SecurityGroupIds: config.VPCConfig.SecurityGroupIDs,
		}
	}

	// Create function with retry
	var function *lambda.CreateFunctionOutput
	err = retry.RetryContext(ctx, 5*time.Minute, func() *retry.RetryError {
		var err error
		function, err = c.Lambda.CreateFunction(ctx, createInput)
		if err != nil {
			if strings.Contains(err.Error(), "InvalidParameterValueException") {
				// IAM role propagation delay
				return retry.RetryableError(err)
			}
			return retry.NonRetryableError(err)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("creating Lambda function: %w", err)
	}

	return function, nil
}

func (c *AWSClient) DeleteLambdaFunction(ctx context.Context, functionName string) error {
	_, err := c.Lambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return fmt.Errorf("deleting Lambda function: %w", err)
		}
	}
	return nil
}

// API Gateway Integration
func (c *AWSClient) CreateAPIGatewayIntegration(ctx context.Context, apiID, functionArn string, route RouteConfig) error {
	// First, create or get the resource
	resourceID, err := c.ensureAPIGatewayResource(ctx, apiID, route.Path)
	if err != nil {
		return err
	}

	// Create method
	_, err = c.APIGateway.PutMethod(ctx, &apigateway.PutMethodInput{
		RestApiId:         aws.String(apiID),
		ResourceId:        aws.String(resourceID),
		HttpMethod:        aws.String(route.Method),
		AuthorizationType: aws.String(route.Authorization),
		AuthorizerId:      aws.String(route.AuthorizerID),
		ApiKeyRequired:    false,
	})
	if err != nil {
		return fmt.Errorf("creating API Gateway method: %w", err)
	}

	// Create integration
	integrationUri := fmt.Sprintf(
		"arn:aws:apigateway:%s:lambda:path/2015-03-31/functions/%s/invocations",
		c.Region,
		functionArn,
	)

	_, err = c.APIGateway.PutIntegration(ctx, &apigateway.PutIntegrationInput{
		RestApiId:             aws.String(apiID),
		ResourceId:            aws.String(resourceID),
		HttpMethod:            aws.String(route.Method),
		Type:                  apigatewaytypes.IntegrationType(route.IntegrationType),
		IntegrationHttpMethod: aws.String("POST"),
		Uri:                   aws.String(integrationUri),
		Credentials: aws.String(fmt.Sprintf(
			"arn:aws:iam::%s:role/apig-lambda-execution",
			c.AccountID,
		)),
	})
	if err != nil {
		return fmt.Errorf("creating API Gateway integration: %w", err)
	}

	// Create integration response
	_, err = c.APIGateway.PutIntegrationResponse(ctx, &apigateway.PutIntegrationResponseInput{
		RestApiId:        aws.String(apiID),
		ResourceId:       aws.String(resourceID),
		HttpMethod:       aws.String(route.Method),
		StatusCode:       aws.String("200"),
		SelectionPattern: aws.String(""),
	})
	if err != nil {
		return fmt.Errorf("creating integration response: %w", err)
	}

	// Create method response
	_, err = c.APIGateway.PutMethodResponse(ctx, &apigateway.PutMethodResponseInput{
		RestApiId:  aws.String(apiID),
		ResourceId: aws.String(resourceID),
		HttpMethod: aws.String(route.Method),
		StatusCode: aws.String("200"),
		ResponseModels: map[string]string{
			"application/json": "Empty",
		},
	})
	if err != nil {
		return fmt.Errorf("creating method response: %w", err)
	}

	return nil
}

func (c *AWSClient) ensureAPIGatewayResource(ctx context.Context, apiID, path string) (string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	currentResourceID := ""

	for _, part := range parts {
		// Get parent resources
		resources, err := c.APIGateway.GetResources(ctx, &apigateway.GetResourcesInput{
			RestApiId: aws.String(apiID),
		})
		if err != nil {
			return "", fmt.Errorf("getting API Gateway resources: %w", err)
		}

		var foundResourceID string
		for _, resource := range resources.Items {
			if resource.PathPart != nil && *resource.PathPart == part {
				if currentResourceID == "" || (resource.ParentId != nil && *resource.ParentId == currentResourceID) {
					foundResourceID = aws.ToString(resource.Id)
					break
				}
			}
		}

		if foundResourceID == "" {
			// Create resource
			createInput := &apigateway.CreateResourceInput{
				RestApiId: aws.String(apiID),
				PathPart:  aws.String(part),
			}
			if currentResourceID != "" {
				createInput.ParentId = aws.String(currentResourceID)
			}

			newResource, err := c.APIGateway.CreateResource(ctx, createInput)
			if err != nil {
				return "", fmt.Errorf("creating API Gateway resource %s: %w", part, err)
			}
			currentResourceID = aws.ToString(newResource.Id)
		} else {
			currentResourceID = foundResourceID
		}
	}

	return currentResourceID, nil
}

func (c *AWSClient) DeployAPIGateway(ctx context.Context, apiID, stageName string) error {
	_, err := c.APIGateway.CreateDeployment(ctx, &apigateway.CreateDeploymentInput{
		RestApiId: aws.String(apiID),
		StageName: aws.String(stageName),
	})
	if err != nil {
		return fmt.Errorf("deploying API Gateway: %w", err)
	}
	return nil
}

// CloudWatch Logs
func (c *AWSClient) CreateCloudWatchLogGroup(ctx context.Context, functionName string, retentionDays int32) (string, error) {
	logGroupName := fmt.Sprintf("/aws/lambda/%s", functionName)

	// Check if log group exists
	_, err := c.CloudWatch.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(logGroupName),
	})
	if err != nil {
		// Create log group
		_, err = c.CloudWatch.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
			LogGroupName: aws.String(logGroupName),
			Tags: map[string]string{
				"ManagedBy": "terraform-provider-raysouz",
			},
		})
		if err != nil {
			return "", fmt.Errorf("creating CloudWatch log group: %w", err)
		}
	}

	// Set retention policy
	_, err = c.CloudWatch.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		LogGroupName:    aws.String(logGroupName),
		RetentionInDays: aws.Int32(retentionDays),
	})
	if err != nil {
		return "", fmt.Errorf("setting log retention policy: %w", err)
	}

	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", c.Region, c.AccountID, logGroupName), nil
}

func (c *AWSClient) DeleteCloudWatchLogGroup(ctx context.Context, functionName string) error {
	logGroupName := fmt.Sprintf("/aws/lambda/%s", functionName)
	_, err := c.CloudWatch.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
		LogGroupName: aws.String(logGroupName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return fmt.Errorf("deleting CloudWatch log group: %w", err)
		}
	}
	return nil
}

// SSM Parameters
func (c *AWSClient) CreateSSMParameter(ctx context.Context, param SSMParameter) error {
	input := &ssm.PutParameterInput{
		Name:      aws.String(param.Name),
		Type:      ssmtypes.ParameterType(param.Type),
		Value:     aws.String(param.Value),
		Overwrite: aws.Bool(true),
		Tags: []ssmtypes.Tag{
			{
				Key:   aws.String("ManagedBy"),
				Value: aws.String("terraform-provider-raysouz"),
			},
		},
	}

	if param.Description != "" {
		input.Description = aws.String(param.Description)
	}

	if param.Tier != "" {
		input.Tier = ssmtypes.ParameterTier(param.Tier)
	}

	if param.Type == "SecureString" && param.KeyID != "" {
		input.KeyId = aws.String(param.KeyID)
	}

	_, err := c.SSM.PutParameter(ctx, input)
	if err != nil {
		return fmt.Errorf("creating SSM parameter %s: %w", param.Name, err)
	}

	return nil
}

func (c *AWSClient) DeleteSSMParameter(ctx context.Context, paramName string) error {
	_, err := c.SSM.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(paramName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ParameterNotFound") {
			return fmt.Errorf("deleting SSM parameter %s: %w", paramName, err)
		}
	}
	return nil
}

// Lambda Permissions
func (c *AWSClient) AddLambdaPermission(ctx context.Context, functionName, apiID, sourceArn string) error {
	statementID := fmt.Sprintf("apigateway-%s", apiID)

	_, err := c.Lambda.AddPermission(ctx, &lambda.AddPermissionInput{
		FunctionName: aws.String(functionName),
		StatementId:  aws.String(statementID),
		Action:       aws.String("lambda:InvokeFunction"),
		Principal:    aws.String("apigateway.amazonaws.com"),
		SourceArn:    aws.String(sourceArn),
	})

	return err
}

func (c *AWSClient) RemoveLambdaPermission(ctx context.Context, functionName, apiID string) error {
	statementID := fmt.Sprintf("apigateway-%s", apiID)

	_, err := c.Lambda.RemovePermission(ctx, &lambda.RemovePermissionInput{
		FunctionName: aws.String(functionName),
		StatementId:  aws.String(statementID),
	})

	return err
}
