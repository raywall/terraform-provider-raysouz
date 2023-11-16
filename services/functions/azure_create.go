package functions

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/functions/mgmt/2019-08-01/functions"
	"github.com/Azure/go-autorest/autorest/azure/auth"
)

func CreateAzureFunction(functionAppName, functionName, functionCode string) error {
	// Criar um cliente de autenticação Azure
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err != nil {
		return err
	}

	// Criar o cliente Azure Functions
	functionsClient := functions.NewAppsClient("<sua assinatura Azure>")
	functionsClient.Authorizer = authorizer

	// Parâmetros para criar a função Azure
	functionParams := functions.Function{
		Name:       &functionName,
		ScriptFile: &functionCode,        // Código da função em formato de arquivo
		EntryPoint: azure.String("main"), // Substitua pelo nome da sua função principal
		// Outros parâmetros conforme necessário
	}

	// Chamar a API para criar a função Azure
	_, err = functionsClient.CreateOrUpdateFunction(context.Background(), "<seu grupo de recursos>", functionAppName, functionName, functionParams)
	if err != nil {
		return err
	}

	fmt.Printf("Função Azure '%s' criada com sucesso.\n", functionName)
	return nil
}
