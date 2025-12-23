package types

// LambdaConfig DTO armazena todas as configurações de uma função Lambda.
type LambdaConfig struct {
	FunctionName string            `json:"function_name"`
	Handler      string            `json:"handler"`
	Runtime      string            `json:"runtime"`
	Timeout      int32             `json:"timeout"`
	MemorySize   int32             `json:"memory_size"`
	ZipPath      string            `json:"zip_file,omitempty"`
	S3Bucket     string            `json:"s3_bucket,omitempty"`
	S3Key        string            `json:"s3_key,omitempty"`
	Environment  map[string]string `json:"environment_variables,omitempty"`
	PolicyARNs   []string          `json:"attached_policy_arns,omitempty"`
	ForceUpdate  bool              `json:"force_update,omitempty"`
}
