package service

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
)

// IAMService manipula a lógica de negócio para Roles e Policies.
type IAMService struct {
	IAMRepo *repository.IAMRepository
}

// CheckRoleExists é um método de leitura de estado exposto ao orquestrador/resource.
func (s *IAMService) CheckRoleExists(ctx context.Context, roleName string) (bool, error) {
	role, err := s.IAMRepo.GetRole(ctx, roleName)
	if err != nil {
		return false, err // Erro de API/SDK
	}
	return role != nil, nil // Retorna se a Role existe (true/false)
}

// EnsureRole garante que a Role exista e anexa as políticas necessárias.
func (s *IAMService) EnsureRole(ctx context.Context, functionName string, policyARNs []string) (string, error) {
	roleName := fmt.Sprintf("%s-execution-role", functionName)

	role, err := s.IAMRepo.GetRole(ctx, roleName)
	if err != nil {
		return "", err
	}

	var roleArn *string
	if role == nil {
		// Role não existe, cria.
		roleArn, err = s.IAMRepo.CreateRole(ctx, roleName)
		if err != nil {
			return "", err
		}
	} else {
		roleArn = role.Arn
	}

	// Anexa a política base (CloudWatch Logs)
	basePolicyArn := "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
	if err := s.IAMRepo.AttachPolicy(ctx, roleName, basePolicyArn); err != nil {
		return "", err
	}

	// Anexa as políticas customizadas.
	for _, arn := range policyARNs {
		if err := s.IAMRepo.AttachPolicy(ctx, roleName, arn); err != nil {
			return "", fmt.Errorf("failed to attach policy %s: %w", arn, err)
		}
	}

	// Regra de negócio: Espera pela propagação da Role.
	fmt.Printf("[Service] Waiting 10s for IAM role propagation: %s\n", roleName)
	time.Sleep(10 * time.Second)

	return aws.ToString(roleArn), nil
}

// DeleteRoleAndPolicies desanexa as políticas e deleta a Role.
func (s *IAMService) DeleteRoleAndPolicies(ctx context.Context, roleName string, policyARNs []string) error {
	basePolicyArn := "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"

	// Desanexa políticas customizadas
	for _, arn := range policyARNs {
		s.IAMRepo.DetachPolicy(ctx, roleName, arn)
	}

	// Desanexa política base
	s.IAMRepo.DetachPolicy(ctx, roleName, basePolicyArn)

	// Deleta Role
	return s.IAMRepo.DeleteRole(ctx, roleName)
}
