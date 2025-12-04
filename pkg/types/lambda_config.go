package types

// LambdaConfig DTO armazena todas as configurações de uma função Lambda.
type LambdaConfig struct {
	FunctionName string
	Runtime      string
	Handler      string
	ZipPath      string
	MemorySize   int32
	Timeout      int32
	PolicyARNs   []string          // attached_policy_arns
	Environment  map[string]string // environment_variables
}