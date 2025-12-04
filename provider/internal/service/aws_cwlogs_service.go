package service

import (
	"context"
	"fmt"

	"github.com/raywall/terraform-provider-raysouz/provider/internal/repository"
)

// CWLogsService manipula a lógica de negócio para CloudWatch Logs.
type CWLogsService struct {
	CWLogsRepo *repository.CWLogsRepository
}

// EnsureLogGroup garante que o Log Group da Lambda exista e define a retenção.
func (s *CWLogsService) EnsureLogGroup(ctx context.Context, functionName string, retentionDays int32) (string, error) {
	logGroupName := fmt.Sprintf("/aws/lambda/%s", functionName)

	err := s.CWLogsRepo.CreateLogGroupIfNotExists(ctx, logGroupName, retentionDays)
	if err != nil {
		return "", err
	}
	return logGroupName, nil
}
