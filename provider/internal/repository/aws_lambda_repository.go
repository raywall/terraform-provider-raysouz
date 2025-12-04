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
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	dto "github.com/raywall/terraform-provider-raysouz/pkg/types"
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
	// 1. Lógica de leitura de código e runtime
	bs, rerr := os.ReadFile(lc.ZipPath)
	if rerr != nil {
		return nil, fmt.Errorf("reading zip file: %w", rerr)
	}
	rt := mapRuntime(lc.Runtime)

	got, err := r.GetFunction(ctx, lc.FunctionName)

	if got != nil && err == nil {
		// Função existe: Faz o UPDATE (Configuração + Código)
		if err := r.updateFunctionConfiguration(ctx, lc, roleArn, rt); err != nil {
			return nil, err
		}
		if err := r.updateFunctionCode(ctx, lc.FunctionName, bs); err != nil {
			return nil, err
		}

		// Aguarda o status Ativo/Atualizado
		if werr := r.waitForActive(ctx, lc.FunctionName); werr != nil {
			return nil, werr
		}
		return got.FunctionArn, nil
	}

	// Função não existe: Faz o CREATE
	result, cerr := r.Client.Lambda.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: aws.String(lc.FunctionName),
		Role:         aws.String(roleArn),
		Handler:      aws.String(lc.Handler),
		Runtime:      rt,
		Code:         &types.FunctionCode{ZipFile: bs},
		MemorySize:   aws.Int32(lc.MemorySize),
		Timeout:      aws.Int32(lc.Timeout),
		Environment: &types.Environment{
			Variables: lc.Environment,
		},
	})

	if cerr != nil {
		if strings.Contains(cerr.Error(), "ResourceConflictException") {
			g2, _ := r.GetFunction(ctx, lc.FunctionName)
			if g2 != nil {
				return g2.FunctionArn, nil
			}
		}
		return nil, cerr
	}

	if result != nil {
		return result.FunctionArn, nil
	}
	return nil, fmt.Errorf("lambda created but ARN not available")
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
