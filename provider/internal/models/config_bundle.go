package models

import (
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/service"
)

// ConfigurationBundle contém os Services e o Cliente AWS para serem injetados nos Resources.
// M é a interface (interface{}) que os recursos recebem.
type ConfigurationBundle struct {
	DeployService *service.LambdaDeploymentService
	Client        *client.AWSClient
}
