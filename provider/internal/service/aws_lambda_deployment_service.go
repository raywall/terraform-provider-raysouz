package service

import (
	"context"
	"fmt"

	"github.com/raywall/terraform-provider-raysouz/pkg/types"
	dto "github.com/raywall/terraform-provider-raysouz/pkg/types"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
)

// LambdaDeploymentService Orquestrador de Deploy
type LambdaDeploymentService struct {
	IAMService        *IAMService
	CWLogsService     *CWLogsService
	APIGatewayService *APIGatewayService
	LambdaRepo        *repository.LambdaRepository
	Client            *client.AWSClient
}

// CheckResourceExistence verifica se os componentes principais do recurso existem na AWS.
func (s *LambdaDeploymentService) CheckResourceExistence(ctx context.Context, st *types.ResourceState) (bool, error) {
	// 1. Verificar Role (Chama o IAM Service)
	roleExists, err := s.IAMService.CheckRoleExists(ctx, st.RoleName)
	if err != nil {
		return false, err
	}
	if !roleExists {
		return false, nil
	}

	// 2. Verificar Lambda (Chama o Lambda Repo/Service)
	fnConfig, err := s.LambdaRepo.GetFunction(ctx, st.FunctionName)
	if err != nil {
		return false, err
	}
	if fnConfig == nil {
		return false, nil
	}

	// Assumimos que se a Role e a Função existem, o recurso existe (para o READ)
	return true, nil
}

// EnsureDeployment orquestra toda a criação ou atualização do recurso.
func (s *LambdaDeploymentService) EnsureDeployment(ctx context.Context, apiID, stage string, lc *dto.LambdaConfig, routes []dto.RouteConfig) (*dto.ResourceState, error) {
	// 1. GARANTIR ROLE (Chama o Service IAM)
	roleArn, err := s.IAMService.EnsureRole(ctx, lc.FunctionName, lc.PolicyARNs)
	if err != nil {
		return nil, fmt.Errorf("IAM role setup failed: %w", err)
	}
	// O IAMService já cuidou da espera de propagação (time.Sleep).

	// 2. GARANTIR LAMBDA
	fnArn, err := s.LambdaRepo.EnsureFunction(ctx, lc, roleArn)
	if err != nil {
		return nil, fmt.Errorf("Lambda function setup failed: %w", err)
	}

	// 3. GARANTIR LOG GROUP (Chama o Service CWLogs)
	logGroup, err := s.CWLogsService.EnsureLogGroup(ctx, lc.FunctionName, 14) // 14 dias de retenção
	if err != nil {
		return nil, fmt.Errorf("Log group setup failed: %w", err)
	}

	// 4. GARANTIR PERMISSÃO DA LAMBDA
	statementID := fmt.Sprintf("apigateway-%s", apiID)
	sourceArn := fmt.Sprintf("arn:aws:execute-api:%s:%s:%s/*/*/*", s.Client.Region, s.Client.AccountID, apiID)
	if err := s.LambdaRepo.AddPermission(ctx, lc.FunctionName, statementID, sourceArn); err != nil {
		return nil, fmt.Errorf("Lambda permission failed: %w", err)
	}

	// 5. GARANTIR ROTAS APIGW E DEPLOY (Chama o Service APIGW)
	apigwState, err := s.APIGatewayService.EnsureRoutesAndDeploy(ctx, apiID, stage, *fnArn, lc.FunctionName, routes)
	if err != nil {
		return nil, fmt.Errorf("APIGW route setup failed: %w", err)
	}

	// 6. Criar e retornar o estado final
	return &types.ResourceState{
		RoleName:           fmt.Sprintf("%s-execution-role", lc.FunctionName),
		FunctionName:       lc.FunctionName,
		FunctionArn:        fnArn,
		APIGatewayID:       apigwState.APIGatewayID,
		StageName:          apigwState.StageName,
		Routes:             apigwState.Routes,
		LogGroup:           logGroup,
		Resources:          apigwState.Resources,
		AttachedPolicyARNs: lc.PolicyARNs,
	}, nil
}

// DeleteDeployment orquestra a exclusão completa dos recursos.
func (s *LambdaDeploymentService) DeleteDeployment(ctx context.Context, st *dto.ResourceState) error {
	var errors []error

	// 1. Deletar Rotas APIGW
	if err := s.APIGatewayService.DeleteRoutesOrchestration(ctx, st.APIGatewayID, st.Routes, st.Resources); err != nil {
		errors = append(errors, fmt.Errorf("APIGW deletion failed: %w", err))
	}

	// 2. Remover Permissão Lambda
	statementID := fmt.Sprintf("apigateway-%s", st.APIGatewayID)
	s.LambdaRepo.RemovePermission(ctx, st.FunctionName, statementID) // Não falha em erro

	// 3. Deletar Lambda
	if err := s.LambdaRepo.DeleteFunction(ctx, st.FunctionName); err != nil {
		errors = append(errors, fmt.Errorf("Lambda deletion failed: %w", err))
	}

	// 4. Deletar Role IAM (Chama o Service IAM)
	if err := s.IAMService.DeleteRoleAndPolicies(ctx, st.RoleName, st.AttachedPolicyARNs); err != nil {
		errors = append(errors, fmt.Errorf("IAM role deletion failed: %w", err))
	}

	// 5. Deletar Log Group (Chama o Service CWLogs)
	s.CWLogsService.CWLogsRepo.DeleteLogGroup(ctx, st.LogGroup) // Não falha em erro

	if len(errors) > 0 {
		return fmt.Errorf("multiple errors during deletion: %v", errors)
	}

	return nil
}
