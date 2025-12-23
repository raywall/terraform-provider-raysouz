package types

// ResourceState é a estrutura principal que armazena todo o estado interno
// do recurso 'raysouz_apigateway_lambda_routes'.
type ResourceState struct {
	RoleName           string                  `json:"role_name"`
	FunctionName       string                  `json:"function_name"`
	FunctionArn        *string                 `json:"function_arn"`
	APIGatewayID       string                  `json:"api_gateway_id"`
	StageName          string                  `json:"stage_name"`
	Routes             []RouteState            `json:"routes"`
	LogGroup           string                  `json:"log_group"`
	Resources          map[string]ResourceInfo `json:"resources"`
	AttachedPolicyARNs []string                `json:"attached_policy_arns"`
	LambdaConfig       LambdaConfig            `json:"lambda_config,omitempty"`
	CodeHash           string                  `json:"code_hash,omitempty"`
	LastUpdateTime     int64                   `json:"last_update_time,omitempty"`
}
