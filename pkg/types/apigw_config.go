package types

// APIGWState armazena o estado de recursos APIGW complexos.
type APIGWState struct {
	APIGatewayID string
	StageName    string
	Routes       []RouteState
	Resources    map[string]ResourceInfo // Usa DTO de baixo nível do Repositório
}