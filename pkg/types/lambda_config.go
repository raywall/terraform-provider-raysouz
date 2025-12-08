package types

// LambdaConfig DTO armazena todas as configurações de uma função Lambda.
type LambdaConfig struct {
	FunctionName string
	Handler      string
	Runtime      string
	Timeout      int32
	MemorySize   int32
	ZipPath      string
	S3Bucket     string
	S3Key        string
	Environment  map[string]string // environment_variables
	PolicyARNs   []string          // attached_policy_arns
}
