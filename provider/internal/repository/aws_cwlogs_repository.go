package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
)

// CWLogsRepository encapsula operações CRUD da AWS CloudWatch Logs.
type CWLogsRepository struct {
	Client *client.AWSClient
}

// CreateLogGroupIfNotExists cria um log group se ele não existir
func (r *CWLogsRepository) CreateLogGroupIfNotExists(ctx context.Context, groupName string, retentionDays int32) error {
	_, err := r.Client.CWLogs.CreateLogGroup(ctx, &cw.CreateLogGroupInput{
		LogGroupName: aws.String(groupName),
	})

	// CORREÇÃO: Verifica se err é nil ANTES de chamar isAPIErrorCode
	if err == nil {
		// Log group criado com sucesso
		return nil
	}

	if isAPIErrorCode(err, "ResourceAlreadyExistsException") {
		// Log group já existe, não é um erro
		return nil
	}

	return fmt.Errorf("creating log group: %w", err)
}

// GetLogGroup recupera informações sobre um log group
func (r *CWLogsRepository) GetLogGroup(ctx context.Context, groupName string) (*cw.DescribeLogGroupsOutput, error) {
	output, err := r.Client.CWLogs.DescribeLogGroups(ctx, &cw.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(groupName),
	})

	if err != nil {
		return nil, fmt.Errorf("describing log groups: %w", err)
	}

	return output, nil
}

// DeleteLogGroup deleta um log group
func (r *CWLogsRepository) DeleteLogGroup(ctx context.Context, groupName string) error {
	_, err := r.Client.CWLogs.DeleteLogGroup(ctx, &cw.DeleteLogGroupInput{
		LogGroupName: aws.String(groupName),
	})

	if err == nil {
		return nil
	}

	if isAPIErrorCode(err, "ResourceNotFoundException") {
		return nil
	}

	return fmt.Errorf("deleting log group: %w", err)
}

// --- Métodos Privados ---

// retry helper com backoff exponencial
func (r *CWLogsRepository) retry(ctx context.Context, attempts int, initial time.Duration, fn func() error) error {
	sleep := initial
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
			sleep = sleep * 2
		}
	}
	return lastErr
}

// isAPIErrorCode verifica se o erro é um erro da API com o código especificado
func isAPIErrorCode(err error, code string) bool {
	// CORREÇÃO CRÍTICA: Verifica se err é nil primeiro
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == code
	}

	return false
}
