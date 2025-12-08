package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	lambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	dto "github.com/raywall/terraform-provider-raysouz/pkg/types"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
)

// LambdaRepository encapsula operações CRUD da AWS Lambda.
type LambdaRepository struct {
	Client *client.AWSClient
}

// GetFunction busca uma função Lambda. Retorna nil se não for encontrada.
func (r *LambdaRepository) GetFunction(ctx context.Context, functionName string) (*types.FunctionConfiguration, error) {
	out, err := r.Client.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(functionName)})
	if err != nil {
		if strings.Contains(err.Error(), "ResourceNotFoundException") {
			return nil, nil
		}
		return nil, fmt.Errorf("GetFunction failed: %w", err)
	}
	return out.Configuration, nil
}

// EnsureFunction cria ou atualiza a função Lambda.
func (r *LambdaRepository) EnsureFunction(ctx context.Context, lc *dto.LambdaConfig, roleArn string) (*string, error) {
	// 1. Validação: precisa ter ZipPath OU (S3Bucket E S3Key)
	hasLocalZip := lc.ZipPath != ""
	hasS3Source := lc.S3Bucket != "" && lc.S3Key != ""

	if !hasLocalZip && !hasS3Source {
		return nil, fmt.Errorf("must provide either ZipPath or both S3Bucket and S3Key")
	}

	if hasLocalZip && hasS3Source {
		return nil, fmt.Errorf("cannot provide both ZipPath and S3 source (S3Bucket/S3Key)")
	}

	// 2. Prepara o código da função baseado na origem
	var functionCode *types.FunctionCode
	var zipBytes []byte

	if hasLocalZip {
		bs, rerr := os.ReadFile(lc.ZipPath)
		if rerr != nil {
			return nil, fmt.Errorf("reading zip file: %w", rerr)
		}
		zipBytes = bs
		functionCode = &types.FunctionCode{ZipFile: bs}
	} else {
		functionCode = &types.FunctionCode{
			S3Bucket: aws.String(lc.S3Bucket),
			S3Key:    aws.String(lc.S3Key),
		}
	}

	rt := mapRuntime(lc.Runtime)
	got, err := r.GetFunction(ctx, lc.FunctionName)

	if got != nil && err == nil {
		// Função existe: Faz o UPDATE

		// IMPORTANTE: Aguarda a função estar disponível ANTES de qualquer atualização
		if werr := r.waitForActive(ctx, lc.FunctionName); werr != nil {
			return nil, fmt.Errorf("waiting for function to be ready before update: %w", werr)
		}

		// Atualiza a configuração primeiro
		if err := r.updateFunctionConfiguration(ctx, lc, roleArn, rt); err != nil {
			return nil, err
		}

		// Aguarda a configuração ser aplicada
		if werr := r.waitForActive(ctx, lc.FunctionName); werr != nil {
			return nil, fmt.Errorf("waiting after configuration update: %w", werr)
		}

		// Agora atualiza o código
		if hasLocalZip {
			if err := r.updateFunctionCode(ctx, lc.FunctionName, zipBytes); err != nil {
				return nil, err
			}
		} else {
			if err := r.updateFunctionCodeFromS3(ctx, lc.FunctionName, lc.S3Bucket, lc.S3Key); err != nil {
				return nil, err
			}
		}

		// Aguarda o código ser atualizado
		if werr := r.waitForActive(ctx, lc.FunctionName); werr != nil {
			return nil, fmt.Errorf("waiting after code update: %w", werr)
		}

		return got.FunctionArn, nil
	}

	// Função não existe: Faz o CREATE
	result, cerr := r.Client.Lambda.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: aws.String(lc.FunctionName),
		Handler:      aws.String(lc.Handler),
		Runtime:      rt,
		Timeout:      aws.Int32(lc.Timeout),
		MemorySize:   aws.Int32(lc.MemorySize),
		Role:         aws.String(roleArn),
		Code:         functionCode,
		Environment: &types.Environment{
			Variables: lc.Environment,
		},
	})

	if cerr != nil {
		if strings.Contains(cerr.Error(), "ResourceConflictException") {
			// Aguarda um pouco e tenta recuperar
			time.Sleep(2 * time.Second)
			if werr := r.waitForActive(ctx, lc.FunctionName); werr != nil {
				return nil, fmt.Errorf("function creation conflict, wait failed: %w", werr)
			}
			g2, _ := r.GetFunction(ctx, lc.FunctionName)
			if g2 != nil {
				return g2.FunctionArn, nil
			}
		}
		return nil, cerr
	}

	if result != nil {
		// Aguarda a função criada estar ativa
		if werr := r.waitForActive(ctx, lc.FunctionName); werr != nil {
			return nil, fmt.Errorf("waiting after function creation: %w", werr)
		}
		return result.FunctionArn, nil
	}
	return nil, fmt.Errorf("lambda created but ARN not available")
}

// updateFunctionCodeFromS3 atualiza o código da Lambda a partir do S3
func (r *LambdaRepository) updateFunctionCodeFromS3(ctx context.Context, functionName, s3Bucket, s3Key string) error {
	_, err := r.Client.Lambda.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		S3Bucket:     aws.String(s3Bucket),
		S3Key:        aws.String(s3Key),
	})

	if err != nil {
		return fmt.Errorf("updating function code from S3: %w", err)
	}

	return nil
}

// waitForActive aguarda a função Lambda estar no estado Active
func (r *LambdaRepository) waitForActive(ctx context.Context, functionName string) error {
	maxRetries := 60 // 60 tentativas
	retryDelay := 2 * time.Second

	for i := 0; i < maxRetries; i++ {
		resp, err := r.Client.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String(functionName),
		})

		if err != nil {
			return fmt.Errorf("checking function status: %w", err)
		}

		state := resp.Configuration.State
		lastUpdateStatus := resp.Configuration.LastUpdateStatus

		// Estados considerados "prontos"
		if state == types.StateActive && lastUpdateStatus == types.LastUpdateStatusSuccessful {
			return nil
		}

		// Estados de falha
		if state == types.StateFailed || lastUpdateStatus == types.LastUpdateStatusFailed {
			return fmt.Errorf("function in failed state: %s (last update: %s)", state, lastUpdateStatus)
		}

		// Aguarda antes da próxima tentativa
		time.Sleep(retryDelay)
	}

	return fmt.Errorf("timeout waiting for function to become active after %d retries", maxRetries)
}

// AddPermission adiciona permissão de invocação (usado para APIGW).
func (r *LambdaRepository) AddPermission(ctx context.Context, functionName, apiID, sourceArn string) error {
	statementID := fmt.Sprintf("apigateway-%s", apiID)

	_, err := r.Client.Lambda.AddPermission(ctx, &lambda.AddPermissionInput{
		FunctionName: aws.String(functionName),
		StatementId:  aws.String(statementID),
		Action:       aws.String("lambda:InvokeFunction"),
		Principal:    aws.String("apigateway.amazonaws.com"),
		SourceArn:    aws.String(sourceArn),
	})

	if err != nil && !strings.Contains(err.Error(), "ResourceConflictException") {
		return fmt.Errorf("AddPermission failed: %w", err)
	}
	return nil
}

// RemovePermission remove permissão.
func (r *LambdaRepository) RemovePermission(ctx context.Context, functionName, apiID string) error {
	statementID := fmt.Sprintf("apigateway-%s", apiID)
	_, err := r.Client.Lambda.RemovePermission(ctx, &lambda.RemovePermissionInput{
		FunctionName: aws.String(functionName),
		StatementId:  aws.String(statementID),
	})
	if err != nil && !strings.Contains(err.Error(), "ResourceNotFoundException") {
		return fmt.Errorf("RemovePermission failed: %w", err)
	}
	return nil
}

// DeleteFunction deleta a Lambda.
func (r *LambdaRepository) DeleteFunction(ctx context.Context, functionName string) error {
	_, err := r.Client.Lambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil && !strings.Contains(err.Error(), "ResourceNotFoundException") {
		return fmt.Errorf("DeleteFunction failed: %w", err)
	}
	return nil
}

// --- Métodos Privados ---

func (r *LambdaRepository) updateFunctionConfiguration(ctx context.Context, lc *dto.LambdaConfig, roleArn string, rt types.Runtime) error {
	_, uerr := r.Client.Lambda.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(lc.FunctionName),
		Role:         aws.String(roleArn),
		Handler:      aws.String(lc.Handler),
		Runtime:      rt,
		MemorySize:   aws.Int32(lc.MemorySize),
		Timeout:      aws.Int32(lc.Timeout),
		Environment: &types.Environment{
			Variables: lc.Environment,
		},
	})
	if uerr != nil {
		return fmt.Errorf("failed to update lambda configuration: %w", uerr)
	}
	return nil
}

func (r *LambdaRepository) updateFunctionCode(ctx context.Context, functionName string, bs []byte) error {
	_, upCodeErr := r.Client.Lambda.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		ZipFile:      bs,
	})
	if upCodeErr != nil {
		return fmt.Errorf("failed to update lambda code: %w", upCodeErr)
	}
	return nil
}

func (r *LambdaRepository) waitForActive(ctx context.Context, functionName string) error {
	waiter := lambda.NewFunctionActiveWaiter(r.Client.Lambda)

	// Usamos GetFunctionConfigurationInput devido ao erro de compilação anterior
	waiterErr := waiter.Wait(ctx, &lambda.GetFunctionConfigurationInput{FunctionName: aws.String(functionName)}, 30*time.Second)

	if waiterErr != nil {
		// Loga o aviso, mas tenta uma checagem final.
		if _, checkErr := r.GetFunction(ctx, functionName); checkErr != nil {
			return fmt.Errorf("function update wait failed and final check failed: %w", checkErr)
		}
		// Se a checagem final passar, ignoramos o erro de timeout do waiter.
		fmt.Printf("Warning: function update wait failed but final check passed: %v\n", waiterErr)
	}
	return nil
}

func mapRuntime(runtime string) types.Runtime {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "provided.al2", "providedal2":
		return types.RuntimeProvidedal2
	case "provided.al2023", "providedal2023":
		return types.RuntimeProvidedal2023
	case "python3.12":
		return types.RuntimePython312
	case "nodejs20.x":
		return types.RuntimeNodejs20x
	default:
		return types.Runtime(runtime)
	}
}
