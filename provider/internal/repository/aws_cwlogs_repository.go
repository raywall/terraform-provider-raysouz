package repository

import (
	"context"
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


// CreateLogGroupIfNotExists cria um Log Group e define a retenção.
func (r *CWLogsRepository) CreateLogGroupIfNotExists(ctx context.Context, name string, retentionDays int32) error {
	// Try to create log group; if already exists, ignore
	_, err := r.Client.CWLogs.CreateLogGroup(ctx, &cw.CreateLogGroupInput{
		LogGroupName: &name,
	})
	if err != nil {
		// Se já existe, ignora e continua para definir a retenção
		if !isAPIErrorCode(err, "ResourceAlreadyExistsException") && !isAPIErrorCode(err, "ResourceAlreadyExists") {
			return fmt.Errorf("CreateLogGroup: %w", err)
		}
	}

	// Tenta definir a retenção (pode falhar por eventual consistência)
	err = r.retry(ctx, 6, 300*time.Millisecond, func() error {
		_, perr := r.Client.CWLogs.PutRetentionPolicy(ctx, &cw.PutRetentionPolicyInput{
			LogGroupName:    &name,
			RetentionInDays: &retentionDays,
		})
		// Se a retenção já estiver definida (InvalidParameterException), considera sucesso
		if isAPIErrorCode(perr, "InvalidParameterException") {
			return nil
		}
		return perr
	})
	if err != nil {
		return fmt.Errorf("PutRetentionPolicy failed after retries: %w", err)
	}
	return nil
}

// DeleteLogGroup deleta o Log Group.
func (r *CWLogsRepository) DeleteLogGroup(ctx context.Context, logGroupName string) error {
	_, err := r.Client.CWLogs.DeleteLogGroup(ctx, &cw.DeleteLogGroupInput{
		LogGroupName: aws.String(logGroupName),
	})
	if err != nil && !isAPIErrorCode(err, "ResourceNotFoundException") {
		return fmt.Errorf("DeleteLogGroup failed: %w", err)
	}
	return nil
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

// isAPIErrorCode verifica o código de erro smithy APIError
func isAPIErrorCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if apiErr.ErrorCode() == code {
		return true
	}
	return false
}
