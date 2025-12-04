package repository

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
)

const StateKey = "terraform.tfstate"
const RollbackKey = "terraform.tfstate.rollback"

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

// Faltam os outros repositórios (Lambda, APIGW, CWLogs) para completar.
