package types

// RouteState DTO armazena o estado de uma rota APIGW após o provisionamento.
type RouteState struct {
	Path          string `json:"path"`
	Method        string `json:"method"`
	Authorization string `json:"authorization"`
	AuthorizerID  string `json:"authorizer_id"`
	ResourceID    string `json:"resource_id"` // ID do recurso APIGW
}