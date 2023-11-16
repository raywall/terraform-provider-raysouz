package functions

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
)

func CreateLambdaFunction(functionName, functionCode string) error {
	// Configurar a sessão AWS
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("us-west-2"), // Substitua pela sua região AWS desejada
	}))

	// Criar o cliente Lambda
	svc := lambda.New(sess)

	// Parâmetros para criar a função Lambda
	params := &lambda.CreateFunctionInput{
		FunctionName: aws.String(functionName),
		Runtime:      aws.String("go1.x"), // Substitua pela versão Go desejada
		Handler:      aws.String("main"),  // Substitua pelo nome da sua função principal
		Code: &lambda.FunctionCode{
			ZipFile: []byte(functionCode), // Código da função em formato de arquivo ZIP
		},
		// Outros parâmetros conforme necessário
	}

	// Chamar a API para criar a função Lambda
	_, err := svc.CreateFunction(params)
	if err != nil {
		return err
	}

	fmt.Printf("Função Lambda '%s' criada com sucesso.\n", functionName)
	return nil
}
