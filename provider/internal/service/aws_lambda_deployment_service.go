package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
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
func (s *LambdaDeploymentService) CheckResourceExistence(ctx context.Context, st *dto.ResourceState) (bool, error) {
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
func (s *LambdaDeploymentService) EnsureDeployment(ctx context.Context, apiID, stage string, lc *dto.LambdaConfig, routes []dto.RouteConfig, forceUpdate bool, stateService StateServiceInterface) (*dto.ResourceState, error) {
	// Gerar ID do recurso
	resourceID := fmt.Sprintf("%s/%s", apiID, lc.FunctionName)

	// 1. Obter estado anterior (se existir)
	var oldState *dto.ResourceState
	if stateService != nil {
		if state, err := stateService.GetInternalState(ctx, resourceID); err == nil {
			oldState = state
		}
	}

	// 2. Analisar mudanças
	var routeChanges *StateChanges
	if stateService != nil && oldState != nil {
		routeChanges = stateService.CompareStateChanges(oldState, lc, routes)

		// Se não há mudanças significativas e não forceUpdate, retornar estado existente
		if !routeChanges.LambdaChanged && !routeChanges.CodeChanged &&
			len(routeChanges.RoutesToAdd) == 0 && len(routeChanges.RoutesToRemove) == 0 &&
			len(routeChanges.RoutesToUpdate) == 0 && !forceUpdate {
			return oldState, nil
		}

		// Processar remoção de rotas se necessário
		if len(routeChanges.RoutesToRemove) > 0 && s.APIGatewayService != nil {
			for _, route := range routeChanges.RoutesToRemove {
				// Log apenas - a remoção real será feita pelo APIGatewayService
				fmt.Printf("[Info] Route marked for removal: %s %s\n", route.Method, route.Path)
			}
		}
	}

	// 3. GARANTIR ROLE (Chama o Service IAM)
	roleArn, err := s.IAMService.EnsureRole(ctx, lc.FunctionName, lc.PolicyARNs)
	if err != nil {
		return nil, fmt.Errorf("IAM role setup failed: %w", err)
	}

	// 4. GARANTIR LAMBDA - com suporte a atualização forçada
	needsLambdaUpdate := forceUpdate || (routeChanges != nil && routeChanges.CodeChanged)
	fnArn, err := s.ensureLambdaWithUpdate(ctx, lc, roleArn, needsLambdaUpdate)
	if err != nil {
		return nil, fmt.Errorf("Lambda function setup failed: %w", err)
	}

	// 5. GARANTIR LOG GROUP (Chama o Service CWLogs)
	logGroup, err := s.CWLogsService.EnsureLogGroup(ctx, lc.FunctionName, 14) // 14 dias de retenção
	if err != nil {
		return nil, fmt.Errorf("Log group setup failed: %w", err)
	}

	// 6. GARANTIR PERMISSÃO DA LAMBDA
	statementID := fmt.Sprintf("apigateway-%s", apiID)
	sourceArn := fmt.Sprintf("arn:aws:execute-api:%s:%s:%s/*/*/*", s.Client.Region, s.Client.AccountID, apiID)
	if err := s.LambdaRepo.AddPermission(ctx, lc.FunctionName, statementID, sourceArn); err != nil {
		return nil, fmt.Errorf("Lambda permission failed: %w", err)
	}

	// 7. GARANTIR ROTAS APIGW E DEPLOY (Chama o Service APIGatewayService)
	// Usar as rotas fornecidas (o APIGatewayService deve lidar com atualizações)
	apigwState, err := s.APIGatewayService.EnsureRoutesAndDeploy(ctx, apiID, stage, *fnArn, lc.FunctionName, routes)
	if err != nil {
		return nil, fmt.Errorf("APIGW route setup failed: %w", err)
	}

	// 8. Criar e retornar o estado final
	finalState := &dto.ResourceState{
		RoleName:           fmt.Sprintf("%s-execution-role", lc.FunctionName),
		FunctionName:       lc.FunctionName,
		FunctionArn:        fnArn,
		APIGatewayID:       apigwState.APIGatewayID,
		StageName:          apigwState.StageName,
		Routes:             apigwState.Routes,
		LogGroup:           logGroup,
		Resources:          apigwState.Resources,
		AttachedPolicyARNs: lc.PolicyARNs,
		LambdaConfig:       *lc,
		CodeHash:           calculateCodeHash(lc),
		LastUpdateTime:     time.Now().Unix(),
	}

	// 9. Salvar novo estado
	if stateService != nil {
		if err := stateService.SaveInternalState(ctx, resourceID, finalState); err != nil {
			// Logar erro mas não falhar
			fmt.Printf("Warning: Failed to save internal state: %v\n", err)
		}
	}

	return finalState, nil
}

// ensureLambdaWithUpdate gerencia a criação/atualização da Lambda
func (s *LambdaDeploymentService) ensureLambdaWithUpdate(ctx context.Context, lc *dto.LambdaConfig, roleArn string, needsUpdate bool) (*string, error) {
	// Verificar se a função já existe
	fnConfig, err := s.LambdaRepo.GetFunction(ctx, lc.FunctionName)
	if err != nil {
		return nil, err
	}

	functionExists := fnConfig != nil

	if !functionExists {
		// Criar nova função
		return s.LambdaRepo.EnsureFunction(ctx, lc, roleArn)
	}

	// Se precisa atualizar, chamar EnsureFunction que deve lidar com atualizações
	if needsUpdate {
		// Obter a role atual da função
		currentRole := s.extractFunctionRole(fnConfig)

		// Chamar EnsureFunction que deve atualizar se já existir
		return s.LambdaRepo.EnsureFunction(ctx, lc, currentRole)
	}

	// Se não precisa atualizar, retornar ARN existente
	return s.extractFunctionArn(fnConfig), nil
}

// extractFunctionRole extrai a role de uma FunctionConfiguration
func (s *LambdaDeploymentService) extractFunctionRole(fnConfig *types.FunctionConfiguration) string {
	if fnConfig == nil || fnConfig.Role == nil {
		return ""
	}
	return *fnConfig.Role
}

// extractFunctionArn extrai ARN de uma FunctionConfiguration
func (s *LambdaDeploymentService) extractFunctionArn(fnConfig *types.FunctionConfiguration) *string {
	if fnConfig == nil || fnConfig.FunctionArn == nil {
		return nil
	}
	return fnConfig.FunctionArn
}

// calculateCodeHash calcula um hash para o código
func calculateCodeHash(lc *dto.LambdaConfig) string {
	hasher := sha256.New()

	// Usar informações disponíveis para criar um hash único
	if lc.S3Bucket != "" && lc.S3Key != "" {
		hasher.Write([]byte(lc.S3Bucket))
		hasher.Write([]byte(lc.S3Key))
		hasher.Write([]byte(fmt.Sprintf(":%d", time.Now().Unix())))
	} else if lc.ZipPath != "" {
		hasher.Write([]byte(lc.ZipPath))
		hasher.Write([]byte(fmt.Sprintf(":%d", time.Now().Unix())))
	}

	hasher.Write([]byte(lc.FunctionName))
	hasher.Write([]byte(lc.Handler))
	hasher.Write([]byte(lc.Runtime))

	// Incluir variáveis de ambiente
	for k, v := range lc.Environment {
		hasher.Write([]byte(k + "=" + v))
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// DeleteDeployment orquestra a exclusão completa dos recursos.
func (s *LambdaDeploymentService) DeleteDeployment(ctx context.Context, st *dto.ResourceState, stateService StateServiceInterface) error {
	// Limpar estado interno primeiro
	if stateService != nil {
		resourceID := fmt.Sprintf("%s/%s", st.APIGatewayID, st.FunctionName)
		if err := stateService.DeleteInternalState(ctx, resourceID); err != nil {
			fmt.Printf("Warning: Failed to delete internal state: %v\n", err)
		}
	}

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
