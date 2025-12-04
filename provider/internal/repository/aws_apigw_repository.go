package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	apigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/raywall/terraform-provider-raysouz/provider/internal/client"
	"github.com/raywall/terraform-provider-raysouz/pkg/types"
)

// APIGWRepository encapsula operações CRUD da AWS API Gateway (v1).
type APIGWRepository struct {
	Client *client.AWSClient
}


// GetRootResourceID busca o ID do recurso raiz (/).
func (r *APIGWRepository) GetRootResourceID(ctx context.Context, apiID string) (string, error) {
	result, err := r.Client.APIGW.GetResources(ctx, &apigw.GetResourcesInput{
		RestApiId: aws.String(apiID),
	})
	if err != nil {
		return "", err
	}

	for _, res := range result.Items {
		if aws.ToString(res.Path) == "/" {
			return aws.ToString(res.Id), nil
		}
	}
	return "", fmt.Errorf("root resource not found for API ID: %s", apiID)
}

// EnsurePath cria um recurso APIGW se ele não existir, retornando o ID do recurso final.
func (r *APIGWRepository) EnsurePath(ctx context.Context, apiID, rootID, path string) (string, map[string]types.ResourceInfo, error) {
	path = strings.Trim(path, "/")
	resources := make(map[string]types.ResourceInfo)

	if path == "" {
		return rootID, resources, nil
	}

	parts := strings.Split(path, "/")
	currentParentID := rootID
	currentPath := ""

	for _, part := range parts {
		currentPath = currentPath + "/" + part

		// Tenta encontrar o recurso existente
		existing, err := r.findResourceByPath(ctx, apiID, currentPath)
		if err == nil && existing != "" {
			currentParentID = existing
			resources[currentPath] = types.ResourceInfo{ResourceID: existing, PathPart: part}
			continue
		}

		// Cria o recurso
		result, err := r.Client.APIGW.CreateResource(ctx, &apigw.CreateResourceInput{
			RestApiId: aws.String(apiID),
			ParentId:  aws.String(currentParentID),
			PathPart:  aws.String(part),
		})
		if err != nil {
			if strings.Contains(err.Error(), "ConflictException") {
				existing, _ := r.findResourceByPath(ctx, apiID, currentPath)
				if existing != "" {
					currentParentID = existing
					resources[currentPath] = types.ResourceInfo{ResourceID: existing, PathPart: part}
					continue
				}
			}
			return "", nil, fmt.Errorf("CreateResource failed for path %s: %w", currentPath, err)
		}

		currentParentID = aws.ToString(result.Id)
		resources[currentPath] = types.ResourceInfo{ResourceID: currentParentID, PathPart: part}
	}

	return currentParentID, resources, nil
}

// PutMethodAndIntegration cria o Método e a Integração Lambda Proxy.
func (r *APIGWRepository) PutMethodAndIntegration(ctx context.Context, apiID, resourceID, httpMethod, functionArn, region, authorization, authorizerID string) error {
	// 1. Put Method
	input := &apigw.PutMethodInput{
		RestApiId:         aws.String(apiID),
		ResourceId:        aws.String(resourceID),
		HttpMethod:        aws.String(httpMethod),
		AuthorizationType: aws.String(authorization),
		ApiKeyRequired:    false,
	}

	if authorization != "NONE" && authorizerID != "" && authorizerID != "<nil>" {
		input.AuthorizerId = aws.String(authorizerID)
	}

	_, err := r.Client.APIGW.PutMethod(ctx, input)
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return fmt.Errorf("PutMethod failed: %w", err)
	}

	// 2. Put Integration (AWS_PROXY)
	uri := fmt.Sprintf("arn:aws:apigateway:%s:lambda:path/2015-03-31/functions/%s/invocations", region, functionArn)

	_, err = r.Client.APIGW.PutIntegration(ctx, &apigw.PutIntegrationInput{
		RestApiId:             aws.String(apiID),
		ResourceId:            aws.String(resourceID),
		HttpMethod:            aws.String(httpMethod),
		Type:                  "AWS_PROXY",
		IntegrationHttpMethod: aws.String("POST"),
		Uri:                   aws.String(uri),
	})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return fmt.Errorf("PutIntegration failed: %w", err)
	}

	// 3. Put Method Response (200)
	_, err = r.Client.APIGW.PutMethodResponse(ctx, &apigw.PutMethodResponseInput{
		RestApiId:  aws.String(apiID),
		ResourceId: aws.String(resourceID),
		HttpMethod: aws.String(httpMethod),
		StatusCode: aws.String("200"),
	})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return fmt.Errorf("PutMethodResponse failed: %w", err)
	}

	// 4. Put Integration Response (200)
	_, err = r.Client.APIGW.PutIntegrationResponse(ctx, &apigw.PutIntegrationResponseInput{
		RestApiId:  aws.String(apiID),
		ResourceId: aws.String(resourceID),
		HttpMethod: aws.String(httpMethod),
		StatusCode: aws.String("200"),
	})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return fmt.Errorf("PutIntegrationResponse failed: %w", err)
	}

	return nil
}

// DeployAPI cria um deployment.
func (r *APIGWRepository) DeployAPI(ctx context.Context, apiID, stageName string) error {
	_, err := r.Client.APIGW.CreateDeployment(ctx, &apigw.CreateDeploymentInput{
		RestApiId: aws.String(apiID),
		StageName: aws.String(stageName),
	})
	return err
}

// DeleteMethod deleta um método.
func (r *APIGWRepository) DeleteMethod(ctx context.Context, apiID, resourceID, httpMethod string) error {
	_, err := r.Client.APIGW.DeleteMethod(ctx, &apigw.DeleteMethodInput{
		RestApiId:  aws.String(apiID),
		ResourceId: aws.String(resourceID),
		HttpMethod: aws.String(httpMethod),
	})
	if err != nil && !strings.Contains(err.Error(), "NotFoundException") {
		return fmt.Errorf("DeleteMethod failed: %w", err)
	}
	return nil
}

// DeleteResources deleta recursos APIGW na ordem correta (por profundidade).
func (r *APIGWRepository) DeleteResources(ctx context.Context, apiID string, resources map[string]types.ResourceInfo) error {
	if len(resources) == 0 {
		return nil
	}

	// Lógica de ordenação (deepest first)
	type resourceDepth struct {
		resourceID string
		path       string
		depth      int
	}

	var sortedResources []resourceDepth
	for path, res := range resources {
		depth := strings.Count(path, "/")
		sortedResources = append(sortedResources, resourceDepth{
			resourceID: res.ResourceID,
			path:       path,
			depth:      depth,
		})
	}

	// Bubble Sort simples (descendente por profundidade)
	for i := 0; i < len(sortedResources); i++ {
		for j := i + 1; j < len(sortedResources); j++ {
			if sortedResources[j].depth > sortedResources[i].depth {
				sortedResources[i], sortedResources[j] = sortedResources[j], sortedResources[i]
			}
		}
	}

	// Delete recursos em ordem
	for _, res := range sortedResources {
		_, err := r.Client.APIGW.DeleteResource(ctx, &apigw.DeleteResourceInput{
			RestApiId:  aws.String(apiID),
			ResourceId: aws.String(res.resourceID),
		})
		if err != nil {
			if !strings.Contains(err.Error(), "NotFoundException") {
				fmt.Printf("Warning: failed to delete resource %s (%s): %v\n", res.path, res.resourceID, err)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	return nil
}

// findResourceByPath é um helper privado para buscar recursos.
func (r *APIGWRepository) findResourceByPath(ctx context.Context, apiID, path string) (string, error) {
	result, err := r.Client.APIGW.GetResources(ctx, &apigw.GetResourcesInput{
		RestApiId: aws.String(apiID),
	})
	if err != nil {
		return "", err
	}

	for _, res := range result.Items {
		if aws.ToString(res.Path) == path {
			return aws.ToString(res.Id), nil
		}
	}
	return "", fmt.Errorf("resource not found")
}
