package models

import (
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/service"
)

// ConfigurationBundle agrupa todas as dependências necessárias
type ConfigurationBundle struct {
	DeployService *service.LambdaDeploymentService
	StateService  service.StateServiceInterface
	Client        *client.AWSClient
}
