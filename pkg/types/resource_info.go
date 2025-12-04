package types

// ResourceInfo é um DTO de baixo nível usado para rastrear recursos APIGW aninhados.
type ResourceInfo struct {
	ResourceID string `json:"resource_id"`
	PathPart   string `json:"path_part"`
}
