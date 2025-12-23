package types

// APIGWState armazena o estado de recursos APIGW complexos.
type APIGWState struct {
	APIGatewayID string                  `json:"api_gateway_id"`
	StageName    string                  `json:"stage_name"`
	Routes       []RouteState            `json:"routes"`
	Resources    map[string]ResourceInfo `json:"resources"`
}
