package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	apigw "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	iam "github.com/aws/aws-sdk-go-v2/service/iam"
	lambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

// AWSClient holds AWS service clients used by the provider.
type AWSClient struct {
	Config aws.Config
	IAM    *iam.Client
	Lambda *lambda.Client
	CWLogs *cw.Client
	APIGW  *apigw.Client
	STS    *sts.Client
	region string
}

// New creates a new AWSClient for the provided region (if empty, uses default chain)
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

	return &AWSClient{
		Config: cfg,
		IAM:    iam.NewFromConfig(cfg),
		Lambda: lambda.NewFromConfig(cfg),
		CWLogs: cw.NewFromConfig(cfg),
		APIGW:  apigw.NewFromConfig(cfg),
		STS:    sts.NewFromConfig(cfg),
		region: cfg.Region,
	}, nil
}

// retry helper with exponential backoff used for eventual-consistency operations
func retry(ctx context.Context, attempts int, initial time.Duration, fn func() error) error {
	sleep := initial
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		// if context canceled stop early
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
			sleep = sleep * 2
		}
	}
	return lastErr
}

// isAPIErrorCode checks smithy APIError code
func isAPIErrorCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == code
	}
	return false
}

// CreateLogGroupIfNotExists creates a CloudWatch Log Group if it doesn't already exist
// and sets retention with retries to handle eventual consistency.
func (c *AWSClient) CreateLogGroupIfNotExists(ctx context.Context, name string, retentionDays int32) error {
	// Try to create log group; if already exists, ignore and continue to set retention.
	_, err := c.CWLogs.CreateLogGroup(ctx, &cw.CreateLogGroupInput{
		LogGroupName: &name,
	})
	if err != nil {
		// if already exists, continue to set retention
		if !isAPIErrorCode(err, "ResourceAlreadyExistsException") && !isAPIErrorCode(err, "ResourceAlreadyExists") {
			// some other error
			return fmt.Errorf("CreateLogGroup: %w", err)
		}
	}

	// PutRetentionPolicy may fail if group hasn't propagated; retry a few times
	err = retry(ctx, 6, 300*time.Millisecond, func() error {
		_, perr := c.CWLogs.PutRetentionPolicy(ctx, &cw.PutRetentionPolicyInput{
			LogGroupName:    &name,
			RetentionInDays: &retentionDays,
		})
		// If still not found, retry
		if perr != nil {
			// if already set, consider success for some error codes
			if isAPIErrorCode(perr, "InvalidParameterException") {
				return nil
			}
			return perr
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("PutRetentionPolicy failed after retries: %w", err)
	}
	return nil
}
