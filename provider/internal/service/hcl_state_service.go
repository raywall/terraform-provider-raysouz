package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/raywall/terraform-provider-raysouz/pkg/types"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
)

// HCLStateService manipula a lógica de negócio para estado interno e backup/rollback.
type HCLStateService struct {
	StateRepo *repository.StateRepository
}

// HandleStateOperation decide se deve fazer rollback ou backup do statefile Terraform.
func (s *HCLStateService) HandleStateOperation(ctx context.Context, doRollback bool) diag.Diagnostics {
	var diags diag.Diagnostics

	if doRollback {
		// Executa Rollback ANTES de qualquer operação do Terraform
		if rerr := s.StateRepo.RestoreRollbackState(ctx); rerr != nil {
			diags = append(diags, diag.FromErr(fmt.Errorf("failed to restore rollback state: %w", rerr))...)
		}
	} else {
		// Cria Backup ANTES do apply (será executado antes de Create/Update)
		if berr := s.StateRepo.CreateBackupState(ctx); berr != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Warning,
				Summary:  "Failed to create state backup",
				Detail:   fmt.Sprintf("Could not copy current state to rollback file: %v.", berr),
			})
		}
	}
	return diags
}

// SaveInternalState salva o estado interno de um recurso
func (s *HCLStateService) SaveInternalState(ctx context.Context, resourceID string, state *types.ResourceState) error {
	// Calcular hash do código atual antes de salvar
	s.updateCodeHash(state)
	return s.StateRepo.SaveInternalState(ctx, resourceID, state)
}

// GetInternalState obtém o estado interno de um recurso
func (s *HCLStateService) GetInternalState(ctx context.Context, resourceID string) (*types.ResourceState, error) {
	return s.StateRepo.GetInternalState(ctx, resourceID)
}

// DeleteInternalState remove o estado interno de um recurso
func (s *HCLStateService) DeleteInternalState(ctx context.Context, resourceID string) error {
	return s.StateRepo.DeleteInternalState(ctx, resourceID)
}

// CompareStateChanges compara o estado atual com o novo para detectar mudanças
func (s *HCLStateService) CompareStateChanges(oldState *types.ResourceState, newConfig *types.LambdaConfig, newRoutes []types.RouteConfig) *StateChanges {
	changes := &StateChanges{}

	if oldState == nil {
		// Primeira criação - tudo é novo
		changes.LambdaChanged = true
		changes.CodeChanged = true
		changes.RoutesToAdd = newRoutes
		return changes
	}

	// Verificar mudanças na Lambda
	if s.hasLambdaConfigChanged(oldState, newConfig) {
		changes.LambdaChanged = true
	}

	// Verificar mudanças nas rotas
	routeChanges := s.compareRoutes(oldState.Routes, newRoutes)
	changes.RoutesToAdd = routeChanges.ToAdd
	changes.RoutesToRemove = routeChanges.ToRemove
	changes.RoutesToUpdate = routeChanges.ToUpdate

	// Verificar se código mudou
	if s.hasCodeChanged(oldState, newConfig) {
		changes.CodeChanged = true
	}

	return changes
}

// hasLambdaConfigChanged verifica se a configuração da Lambda mudou
func (s *HCLStateService) hasLambdaConfigChanged(oldState *types.ResourceState, newConfig *types.LambdaConfig) bool {
	oldLC := oldState.LambdaConfig

	return oldLC.FunctionName != newConfig.FunctionName ||
		oldLC.Handler != newConfig.Handler ||
		oldLC.Runtime != newConfig.Runtime ||
		oldLC.Timeout != newConfig.Timeout ||
		oldLC.MemorySize != newConfig.MemorySize ||
		s.mapsDifferent(oldLC.Environment, newConfig.Environment) ||
		s.slicesDifferent(oldLC.PolicyARNs, newConfig.PolicyARNs)
}

// hasCodeChanged verifica se o código da Lambda mudou
func (s *HCLStateService) hasCodeChanged(oldState *types.ResourceState, newConfig *types.LambdaConfig) bool {
	// Se forçar atualização
	if newConfig.ForceUpdate {
		return true
	}

	// Comparar hashes
	if oldState.CodeHash == "" {
		return true
	}

	// Calcular novo hash
	newHash := s.calculateCodeHash(newConfig)
	return newHash != oldState.CodeHash
}

// updateCodeHash atualiza o hash do código no estado
func (s *HCLStateService) updateCodeHash(state *types.ResourceState) {
	state.CodeHash = s.calculateCodeHash(&state.LambdaConfig)
	state.LastUpdateTime = time.Now().Unix()
}

// calculateCodeHash calcula um hash para a configuração da Lambda
func (s *HCLStateService) calculateCodeHash(lc *types.LambdaConfig) string {
	hasher := sha256.New()

	// Incluir fonte do código
	if lc.S3Bucket != "" && lc.S3Key != "" {
		hasher.Write([]byte("S3:" + lc.S3Bucket + ":" + lc.S3Key))
		// Incluir timestamp para S3
		hasher.Write([]byte(fmt.Sprintf(":%d", time.Now().Unix())))
	} else if lc.ZipPath != "" {
		hasher.Write([]byte("ZIP:" + lc.ZipPath))
		hasher.Write([]byte(fmt.Sprintf(":%d", time.Now().Unix())))
	}

	// Incluir configurações
	hasher.Write([]byte(lc.FunctionName))
	hasher.Write([]byte(lc.Handler))
	hasher.Write([]byte(lc.Runtime))
	hasher.Write([]byte(fmt.Sprintf("%d", lc.Timeout)))
	hasher.Write([]byte(fmt.Sprintf("%d", lc.MemorySize)))

	// Incluir variáveis de ambiente
	for k, v := range lc.Environment {
		hasher.Write([]byte(k + "=" + v))
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// compareRoutes compara rotas antigas e novas
func (s *HCLStateService) compareRoutes(oldRoutes []types.RouteState, newRoutes []types.RouteConfig) RouteChanges {
	changes := RouteChanges{}

	// Converter RouteState para RouteConfig para comparação
	oldRoutesMap := make(map[string]types.RouteConfig)
	for _, route := range oldRoutes {
		key := route.Path + ":" + route.Method
		oldRoutesMap[key] = types.RouteConfig{
			Path:          route.Path,
			Method:        route.Method,
			Authorization: route.Authorization,
			AuthorizerID:  route.AuthorizerID,
		}
	}

	// Verificar rotas novas
	for _, newRoute := range newRoutes {
		key := newRoute.Path + ":" + newRoute.Method
		if oldRoute, exists := oldRoutesMap[key]; exists {
			// Rota existe, verificar se mudou
			if s.routeConfigChanged(oldRoute, newRoute) {
				changes.ToUpdate = append(changes.ToUpdate, newRoute)
			}
			delete(oldRoutesMap, key) // Remover do mapa
		} else {
			// Nova rota
			changes.ToAdd = append(changes.ToAdd, newRoute)
		}
	}

	// Rotas restantes no mapa antigo devem ser removidas
	for _, oldRoute := range oldRoutesMap {
		// Converter de volta para RouteState para remoção
		changes.ToRemove = append(changes.ToRemove, types.RouteState{
			Path:          oldRoute.Path,
			Method:        oldRoute.Method,
			Authorization: oldRoute.Authorization,
			AuthorizerID:  oldRoute.AuthorizerID,
		})
	}

	return changes
}

// routeConfigChanged verifica se a configuração de uma rota mudou
func (s *HCLStateService) routeConfigChanged(oldRoute, newRoute types.RouteConfig) bool {
	return oldRoute.Authorization != newRoute.Authorization ||
		oldRoute.AuthorizerID != newRoute.AuthorizerID
}

// mapsDifferent compara dois maps
func (s *HCLStateService) mapsDifferent(a, b map[string]string) bool {
	if len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return true
		}
	}
	return false
}

// slicesDifferent compara duas slices
func (s *HCLStateService) slicesDifferent(a, b []string) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return true
		}
	}
	return false
}
