package types

// RouteConfig DTO armazena as configurações de uma rota APIGW.
type RouteConfig struct {
	Path          string `json:"path"`
	Method        string `json:"method"`
	Authorization string `json:"authorization"`
	AuthorizerID  string `json:"authorizer_id"`
}
