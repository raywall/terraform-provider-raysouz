package service

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
)

// HCLStateService manipula a lógica de negócio para backup e rollback do statefile no S3.
type HCLStateService struct {
	StateRepo *repository.StateRepository
}

// HandleStateOperation decide se deve fazer rollback ou backup.
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
