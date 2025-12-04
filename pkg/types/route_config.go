package types

// RouteConfig DTO armazena as configurações de uma rota APIGW.
type RouteConfig struct {
	Path          string
	Method        string
	Authorization string
	AuthorizerID  string
}