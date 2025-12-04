package service

import (
	"context"
	"time"
	"fmt"

	dto "github.com/raywall/terraform-provider-raysouz/pkg/types"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
)

// APIGatewayService manipula a lógica de negócio para API Gateway.
type APIGatewayService struct {
	APIGWRepo *repository.APIGWRepository
	Client    *client.AWSClient // Para obter Region/AccountID
}

// EnsureRoutesAndDeploy garante que todos os caminhos, métodos e integrações estejam configurados e faz o deploy.
func (s *APIGatewayService) EnsureRoutesAndDeploy(ctx context.Context, apiID, stage, functionArn string, functionName string, routes []dto.RouteConfig) (*dto.APIGWState, error) {

	rootID, err := s.APIGWRepo.GetRootResourceID(ctx, apiID)
	if err != nil {
		return nil, fmt.Errorf("getting root resource ID: %w", err)
	}

	apigwState := &dto.APIGWState{
		APIGatewayID: apiID,
		StageName:    stage,
		Resources:    make(map[string]dto.ResourceInfo),
	}
	routesState := make([]dto.RouteState, 0, len(routes))

	for _, r := range routes {
		// 1. Ensure Path
		resourceID, pathResources, err := s.APIGWRepo.EnsurePath(ctx, apiID, rootID, r.Path)
		if err != nil {
			return nil, fmt.Errorf("ensure path %s: %w", r.Path, err)
		}

		// Agrega recursos criados/existentes
		for k, v := range pathResources {
			apigwState.Resources[k] = v
		}

		// 2. Put Method, Integration, Responses
		if err := s.APIGWRepo.PutMethodAndIntegration(
			ctx, apiID, resourceID, r.Method, functionArn, s.Client.Region, r.Authorization, r.AuthorizerID,
		); err != nil {
			return nil, fmt.Errorf("put method/integration %s %s: %w", r.Method, r.Path, err)
		}

		routesState = append(routesState, dto.RouteState{
			Path: r.Path, Method: r.Method, Authorization: r.Authorization, AuthorizerID: r.AuthorizerID, ResourceID: resourceID,
		})
	}

	apigwState.Routes = routesState

	// 3. Deploy API
	if err := s.APIGWRepo.DeployAPI(ctx, apiID, stage); err != nil {
		return nil, fmt.Errorf("deploy api failed: %w", err)
	}

	return apigwState, nil
}

// DeleteRoutesOrchestration deleta métodos e recursos na ordem correta.
func (s *APIGatewayService) DeleteRoutesOrchestration(ctx context.Context, apiID string, routes []dto.RouteState, resources map[string]dto.ResourceInfo) error {
	// 1. Deleta Métodos
	for _, route := range routes {
		s.APIGWRepo.DeleteMethod(ctx, apiID, route.ResourceID, route.Method)
	}
	time.Sleep(500 * time.Millisecond) // Espera por consistência

	// 2. Deleta Recursos
	return s.APIGWRepo.DeleteResources(ctx, apiID, resources)
}
