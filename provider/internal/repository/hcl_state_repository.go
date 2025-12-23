package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/raywall/terraform-provider-raysouz/pkg/types"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
)

const StateKey = "terraform.tfstate"
const RollbackKey = "terraform.tfstate.rollback"
const InternalStatePrefix = "internal-state/"

// StateRepository encapsula a lógica customizada de backup/rollback do statefile S3.
type StateRepository struct {
	Client *client.AWSClient
}

// CreateBackupState copia o estado principal (StateKey) para o estado de rollback (RollbackKey).
func (r *StateRepository) CreateBackupState(ctx context.Context) error {
	if r.Client.S3Bucket == "" {
		return nil
	}

	fmt.Printf("[Raysouz State] Creating rollback state backup in s3://%s/%s...\n", r.Client.S3Bucket, RollbackKey)

	_, err := r.Client.S3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(r.Client.S3Bucket),
		CopySource: aws.String(fmt.Sprintf("/%s/%s", r.Client.S3Bucket, StateKey)),
		Key:        aws.String(RollbackKey),
	})
	if err != nil {
		return fmt.Errorf("s3 copy failed: %w", err)
	}
	fmt.Printf("[Raysouz State] Backup created successfully.\n")
	return nil
}

// RestoreRollbackState copia o estado de rollback (RollbackKey) para o estado principal (StateKey).
func (r *StateRepository) RestoreRollbackState(ctx context.Context) error {
	if r.Client.S3Bucket == "" {
		return fmt.Errorf("State bucket not configured for rollback")
	}

	fmt.Printf("[Raysouz State] Restoring rollback state from s3://%s/%s...\n", r.Client.S3Bucket, RollbackKey)

	_, err := r.Client.S3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(r.Client.S3Bucket),
		CopySource: aws.String(fmt.Sprintf("/%s/%s", r.Client.S3Bucket, RollbackKey)),
		Key:        aws.String(StateKey),
	})
	if err != nil {
		return fmt.Errorf("s3 restore failed: %w", err)
	}
	fmt.Printf("[Raysouz State] Rollback restored. Run 'terraform apply' to execute rollback.\n")
	return nil
}

// SaveInternalState salva o estado interno de um recurso no S3
func (r *StateRepository) SaveInternalState(ctx context.Context, resourceID string, state *types.ResourceState) error {
	if r.Client.S3Bucket == "" {
		return fmt.Errorf("S3 bucket not configured for internal state storage")
	}

	key := InternalStatePrefix + resourceID
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// CORREÇÃO: Usar bytes.NewReader em vez de aws.NewReader
	_, err = r.Client.S3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(r.Client.S3Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("failed to save internal state to S3: %w", err)
	}

	return nil
}

// GetInternalState obtém o estado interno de um recurso do S3
func (r *StateRepository) GetInternalState(ctx context.Context, resourceID string) (*types.ResourceState, error) {
	if r.Client.S3Bucket == "" {
		return nil, fmt.Errorf("S3 bucket not configured for internal state storage")
	}

	key := InternalStatePrefix + resourceID
	resp, err := r.Client.S3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.Client.S3Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Se não encontrado, retorna nil (primeira criação)
		return nil, nil
	}
	defer resp.Body.Close()

	var state types.ResourceState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("failed to decode internal state: %w", err)
	}

	return &state, nil
}

// DeleteInternalState remove o estado interno de um recurso do S3
func (r *StateRepository) DeleteInternalState(ctx context.Context, resourceID string) error {
	if r.Client.S3Bucket == "" {
		return nil // Silenciosamente ignora se não configurado
	}

	key := InternalStatePrefix + resourceID
	_, err := r.Client.S3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.Client.S3Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete internal state: %w", err)
	}

	return nil
}
