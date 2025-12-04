package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	iam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
)

// IAMRepository encapsula operações IAM de baixo nível.
type IAMRepository struct {
	Client *client.AWSClient
}

// GetRole busca uma Role IAM. Retorna nil, nil se não for encontrada.
func (r *IAMRepository) GetRole(ctx context.Context, roleName string) (*iamtypes.Role, error) {
	out, err := r.Client.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		// A lógica verifica a string de erro para "NoSuchEntity", que cobre o NoSuchEntityException.
		if strings.Contains(err.Error(), "NoSuchEntity") {
			return nil, nil // Não encontrado
		}

		// REMOVIDO: a declaração de 'nsee' que causava o erro "declared and not used".

		return nil, fmt.Errorf("GetRole failed: %w", err)
	}
	return out.Role, nil
}

// CreateRole cria a Role com a política de confiança Lambda.
func (r *IAMRepository) CreateRole(ctx context.Context, roleName string) (*string, error) {
	assume := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

	cr, cerr := r.Client.IAM.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assume),
	})
	if cerr != nil {
		if strings.Contains(cerr.Error(), "EntityAlreadyExists") {
			r, _ := r.GetRole(ctx, roleName)
			if r != nil {
				return r.Arn, nil
			}
		}
		return nil, cerr
	}

	return cr.Role.Arn, nil
}

// AttachPolicy anexa uma política à Role.
func (r *IAMRepository) AttachPolicy(ctx context.Context, roleName, policyArn string) error {
	_, err := r.Client.IAM.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String(policyArn),
	})
	if err != nil && !strings.Contains(err.Error(), "EntityAlreadyExists") {
		return fmt.Errorf("AttachPolicy failed: %w", err)
	}
	return nil
}

// DetachPolicy desanexa uma política da Role.
func (r *IAMRepository) DetachPolicy(ctx context.Context, roleName, policyArn string) error {
	_, err := r.Client.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String(policyArn),
	})
	if err != nil && !strings.Contains(err.Error(), "NoSuchEntity") {
		return fmt.Errorf("DetachPolicy failed: %w", err)
	}
	return nil
}

// DeleteRole deleta a Role.
func (r *IAMRepository) DeleteRole(ctx context.Context, roleName string) error {
	_, err := r.Client.IAM.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)})
	if err != nil && !strings.Contains(err.Error(), "NoSuchEntity") {
		return fmt.Errorf("DeleteRole failed: %w", err)
	}
	return nil
}
