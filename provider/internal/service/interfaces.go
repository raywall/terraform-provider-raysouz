package service

import (
	"context"

	"github.com/raywall/terraform-provider-raysouz/pkg/types"
)

// StateServiceInterface define a interface para serviços de estado
type StateServiceInterface interface {
	SaveInternalState(ctx context.Context, resourceID string, state *types.ResourceState) error
	GetInternalState(ctx context.Context, resourceID string) (*types.ResourceState, error)
	DeleteInternalState(ctx context.Context, resourceID string) error
	CompareStateChanges(oldState *types.ResourceState, newConfig *types.LambdaConfig, newRoutes []types.RouteConfig) *StateChanges
}

// StateChanges representa as mudanças detectadas entre estados
type StateChanges struct {
	LambdaChanged  bool
	CodeChanged    bool
	RoutesToAdd    []types.RouteConfig
	RoutesToRemove []types.RouteState
	RoutesToUpdate []types.RouteConfig
}

// RouteChanges representa mudanças em rotas
type RouteChanges struct {
	ToAdd    []types.RouteConfig
	ToRemove []types.RouteState
	ToUpdate []types.RouteConfig
}