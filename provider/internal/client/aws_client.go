package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	apigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigwv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	iam "github.com/aws/aws-sdk-go-v2/service/iam"
	lambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	s3 "github.com/aws/aws-sdk-go-v2/service/s3"
	sts "github.com/aws/aws-sdk-go-v2/service/sts"
)

// AWSClient contém clientes e informações de configuração AWS.
type AWSClient struct {
	Config    aws.Config
	IAM       *iam.Client
	Lambda    *lambda.Client
	CWLogs    *cw.Client
	APIGW     *apigw.Client   // REST API (v1)
	APIGWv2   *apigwv2.Client // HTTP API (v2)
	STS       *sts.Client
	S3        *s3.Client // Adicionado para lógica de State
	Region    string
	AccountID string
	S3Bucket  string // Usado para lógica customizada de State/Rollback
}

// New cria um novo AWSClient para a região fornecida.
func New(ctx context.Context, region string) (*AWSClient, error) {
	var cfg aws.Config
	var err error
	if strings.TrimSpace(region) == "" {
		cfg, err = config.LoadDefaultConfig(ctx)
	} else {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
	}
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}

	client := &AWSClient{
		Config:  cfg,
		IAM:     iam.NewFromConfig(cfg),
		Lambda:  lambda.NewFromConfig(cfg),
		CWLogs:  cw.NewFromConfig(cfg),
		APIGW:   apigw.NewFromConfig(cfg),
		APIGWv2: apigwv2.NewFromConfig(cfg),
		STS:     sts.NewFromConfig(cfg),
		S3:      s3.NewFromConfig(cfg),
		Region:  cfg.Region,
	}

	// Pré-carrega o AccountID
	accountID, aerr := getAccountID(ctx, client.STS)
	if aerr != nil {
		return nil, aerr
	}
	client.AccountID = accountID

	return client, nil
}

func getAccountID(ctx context.Context, stsClient *sts.Client) (string, error) {
	result, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("getting account ID: %w", err)
	}
	return aws.ToString(result.Account), nil
}
