# Terraform Provider: raysouz

Provider Terraform para simplificar deployments de Lambda functions com API Gateway.

## Recursos

### `raysouz_apigateway_lambda_routes`

Cria uma função Lambda com rotas no API Gateway existente, incluindo IAM role, CloudWatch logs e parâmetros SSM.

## Instalação

```hcl
terraform {
  required_providers {
    raysouz = {
      source  = "raywall/raysouz"
      version = "~> 0.1.0"
    }
  }
}

provider "raysouz" {
  region = "us-east-1"
  # Opcional: access_key e secret_key
}
```

## Exemplos de Uso

### Exemplo 1: Simples (Sem Layers ou VPC)

```hcl
resource "raysouz_apigateway_lambda_routes" "simple_api" {
  api_gateway_id = aws_api_gateway_rest_api.main.id

  lambda_config {
    function_name = "simple-api-handler"
    runtime       = "python3.11"
    handler       = "index.handler"
    zip_file      = "${path.module}/lambda.zip"
    memory_size   = 256
    timeout       = 30

    environment_variables = {
      ENVIRONMENT = "production"
      LOG_LEVEL   = "INFO"
    }
  }

  log_retention_days = 7

  routes = [
    {
      path   = "/health"
      method = "GET"
    },
    {
      path   = "/users"
      method = "GET"
    },
    {
      path   = "/users/{id}"
      method = "POST"
    }
  ]

  ssm_parameters = [
    {
      name  = "/myapp/api_key"
      type  = "SecureString"
      value = var.api_key
    }
  ]
}
```

### Exemplo 2: Com Layers AWS

```hcl
resource "raysouz_apigateway_lambda_routes" "api_with_layers" {
  api_gateway_id = aws_api_gateway_rest_api.main.id

  lambda_config {
    function_name = "api-with-extensions"
    runtime       = "nodejs20.x"
    handler       = "index.handler"
    zip_file      = "${path.module}/dist/lambda.zip"
    memory_size   = 512
    timeout       = 10

    layers = [
      "arn:aws:lambda:us-east-1:123456789012:layer:AWS-Parameters-and-Secrets-Lambda-Extension:4",
      "arn:aws:lambda:us-east-1:123456789012:layer:Datadog-Extension:50"
    ]
  }

  routes = [
    {
      path   = "/metrics"
      method = "GET"
    }
  ]
}
```

### Exemplo 3: Com VPC Configuração

```hcl
resource "raysouz_apigateway_lambda_routes" "vpc_lambda" {
  api_gateway_id = aws_api_gateway_rest_api.private.id

  lambda_config {
    function_name = "private-api"
    runtime       = "go1.x"
    handler       = "bootstrap"
    zip_file      = "${path.module}/api.zip"
    memory_size   = 1024
    timeout       = 15

    vpc_config {
      subnet_ids = [
        aws_subnet.private_a.id,
        aws_subnet.private_b.id
      ]
      security_group_ids = [
        aws_security_group.lambda_sg.id
      ]
    }
  }

  routes = [
    {
      path   = "/internal/data"
      method = "GET"
      authorization = "AWS_IAM"
    }
  ]
}
```

### Exemplo 4: Com Custom Authorizer

```hcl
resource "raysouz_apigateway_lambda_routes" "auth_api" {
  api_gateway_id = aws_api_gateway_rest_api.auth.id

  lambda_config {
    function_name = "auth-service"
    runtime       = "provided.al2"
    handler       = "bootstrap"
    zip_file      = "auth.zip"
    memory_size   = 256
    timeout       = 5
  }

  routes = [
    {
      path          = "/login"
      method        = "POST"
      authorization = "NONE"
    },
    {
      path          = "/profile"
      method        = "GET"
      authorization = "CUSTOM"
      authorizer_id = aws_api_gateway_authorizer.jwt.id
    },
    {
      path          = "/admin/*"
      method        = "ANY"
      authorization = "COGNITO_USER_POOLS"
    }
  ]
}
```

### Exemplo 5: Múltiplos Parâmetros SSM

```hcl
resource "raysouz_apigateway_lambda_routes" "configurable_api" {
  api_gateway_id = aws_api_gateway_rest_api.config.id

  lambda_config {
    function_name = "config-api"
    runtime       = "python3.10"
    handler       = "main.handler"
    zip_file      = "api.zip"
    memory_size   = 128
    timeout       = 3
  }

  ssm_parameters = [
    {
      name        = "/app/database/host"
      type        = "String"
      value       = "db.example.com"
      description = "Database hostname"
    },
    {
      name        = "/app/database/port"
      type        = "String"
      value       = "5432"
    },
    {
      name        = "/app/database/password"
      type        = "SecureString"
      value       = var.db_password
      description = "Database password"
      tier        = "Advanced"
      key_id      = aws_kms_key.secrets.arn
    },
    {
      name        = "/app/features"
      type        = "StringList"
      value       = "feature1,feature2,feature3"
      description = "Enabled features"
    }
  ]

  routes = [
    {
      path   = "/config"
      method = "GET"
    }
  ]
}
```

### Exemplo 6: API Completa REST

```hcl
resource "raysouz_apigateway_lambda_routes" "rest_api" {
  api_gateway_id = aws_api_gateway_rest_api.products.id

  lambda_config {
    function_name = "products-api"
    runtime       = "java17"
    handler       = "com.example.ProductsHandler::handleRequest"
    zip_file      = "products.jar"
    memory_size   = 2048
    timeout       = 20
  }

  log_retention_days = 30

  routes = [
    {
      path   = "/products"
      method = "GET"
    },
    {
      path   = "/products"
      method = "POST"
      authorization = "AWS_IAM"
    },
    {
      path   = "/products/{id}"
      method = "GET"
    },
    {
      path   = "/products/{id}"
      method = "PUT"
      authorization = "AWS_IAM"
    },
    {
      path   = "/products/{id}"
      method = "DELETE"
      authorization = "AWS_IAM"
    },
    {
      path   = "/products/search"
      method = "POST"
      integration_type = "HTTP"
    }
  ]
}
```

## Argumentos de Referência

### Argumentos Obrigatórios

| Nome             | Tipo   | Descrição                            |
| ---------------- | ------ | ------------------------------------ |
| `api_gateway_id` | string | ID do API Gateway REST API existente |
| `lambda_config`  | bloco  | Configuração da função Lambda        |
| `routes`         | lista  | Rotas para criar no API Gateway      |

### Argumentos Opcionais

| Nome                 | Tipo   | Padrão   | Descrição                              |
| -------------------- | ------ | -------- | -------------------------------------- |
| `log_retention_days` | number | 30       | Dias de retenção de logs no CloudWatch |
| `ssm_parameters`     | lista  | `[]`     | Parâmetros SSM para criar              |
| `stage_name`         | string | `"prod"` | Nome do stage do API Gateway           |

### Bloco `lambda_config`

| Argumento               | Tipo   | Obrigatório | Descrição                              |
| ----------------------- | ------ | ----------- | -------------------------------------- |
| `function_name`         | string | Sim         | Nome da função Lambda                  |
| `runtime`               | string | Sim         | Runtime (python3.11, nodejs20.x, etc.) |
| `handler`               | string | Sim         | Handler function                       |
| `zip_file`              | string | Sim         | Caminho para arquivo ZIP               |
| `memory_size`           | number | Não (128)   | Memória em MB (128-10240)              |
| `timeout`               | number | Não (3)     | Timeout em segundos (1-900)            |
| `environment_variables` | map    | Não         | Variáveis de ambiente                  |
| `layers`                | lista  | Não         | ARNs de Lambda layers                  |
| `vpc_config`            | bloco  | Não         | Configuração de VPC                    |

### Bloco `vpc_config` (dentro de `lambda_config`)

| Argumento            | Tipo  | Obrigatório | Descrição               |
| -------------------- | ----- | ----------- | ----------------------- |
| `subnet_ids`         | lista | Sim         | IDs das subnets         |
| `security_group_ids` | lista | Sim         | IDs dos security groups |

### Bloco `routes`

| Argumento          | Tipo   | Padrão        | Descrição           |
| ------------------ | ------ | ------------- | ------------------- |
| `path`             | string | -             | Caminho da rota     |
| `method`           | string | -             | HTTP method         |
| `authorization`    | string | `"NONE"`      | Tipo de autorização |
| `authorizer_id`    | string | -             | ID do autorizador   |
| `integration_type` | string | `"AWS_PROXY"` | Tipo de integração  |

### Bloco `ssm_parameters`

| Argumento     | Tipo   | Obrigatório        | Descrição                               |
| ------------- | ------ | ------------------ | --------------------------------------- |
| `name`        | string | Sim                | Nome/path do parâmetro                  |
| `type`        | string | Sim                | Tipo (String, StringList, SecureString) |
| `value`       | string | Sim                | Valor do parâmetro                      |
| `description` | string | Não                | Descrição                               |
| `tier`        | string | Não (`"Standard"`) | Tier do parâmetro                       |
| `key_id`      | string | Não                | KMS Key ID para SecureString            |

## Atributos Exportados

| Nome                       | Tipo   | Descrição                       |
| -------------------------- | ------ | ------------------------------- |
| `lambda_function_arn`      | string | ARN da função Lambda criada     |
| `lambda_role_arn`          | string | ARN da IAM role de execução     |
| `cloudwatch_log_group_arn` | string | ARN do grupo de logs CloudWatch |
| `api_execution_arn`        | string | ARN de execução do API Gateway  |
| `created_at`               | string | Timestamp de criação            |
| `last_modified`            | string | Timestamp da última modificação |

## Runtimes Suportados

- `provided.al2` (Custom runtime AL2)
- `provided.al2023` (Custom runtime AL2023)
- `nodejs18.x`, `nodejs20.x`
- `python3.9`, `python3.10`, `python3.11`, `python3.12`
- `java11`, `java17`
- `go1.x`
- `dotnet6`, `dotnet8`
- `ruby3.2`

## Valores de Retenção de Logs

Valores permitidos para `log_retention_days`:
1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1827, 3653

## Métodos HTTP Suportados

GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS, ANY

## Tipos de Autorização

- `NONE` (público)
- `AWS_IAM` (autenticação IAM)
- `CUSTOM` (autorizador customizado)
- `COGNITO_USER_POOLS` (Cognito User Pools)

## Tipos de Integração

- `AWS_PROXY` (padrão para Lambda)
- `AWS`
- `HTTP_PROXY`
- `HTTP`
- `MOCK`

## Dependências

Este provider requer:

1. **API Gateway REST API existente** (não cria um novo)
2. **Arquivo ZIP da função Lambda** localmente acessível
3. **Permissões AWS** adequadas para criar:
   - Funções Lambda
   - IAM Roles e Policies
   - API Gateway Resources/Methods
   - CloudWatch Log Groups
   - SSM Parameters

## Notas Importantes

1. **O API Gateway deve existir** antes de usar este resource
2. **Arquivos ZIP** devem estar no caminho especificado durante `terraform apply`
3. **SSM Parameters** são sobrescritos se já existirem
4. **Rotas** não são removidas automaticamente se modificadas (pode exigir recriação)
5. **Tags** são adicionadas a todos os recursos com `ManagedBy: terraform-provider-raysouz`

## Exemplo Completo com Outputs

```hcl
resource "raysouz_apigateway_lambda_routes" "example" {
  api_gateway_id = aws_api_gateway_rest_api.main.id

  lambda_config {
    function_name = "example-api"
    runtime       = "python3.11"
    handler       = "main.handler"
    zip_file      = "${path.module}/function.zip"
    memory_size   = 256
    timeout       = 10
  }

  routes = [
    {
      path   = "/hello"
      method = "GET"
    }
  ]
}

output "lambda_arn" {
  value = raysouz_apigateway_lambda_routes.example.lambda_function_arn
}

output "api_url" {
  value = "https://${aws_api_gateway_rest_api.main.id}.execute-api.${var.region}.amazonaws.com/prod"
}
```

## Troubleshooting

### Erro: "API Gateway not found"

Verifique se o `api_gateway_id` está correto e se o API Gateway existe.

### Erro: "ZIP file not found"

Verifique o caminho do `zip_file` no `lambda_config`.

### Erro: "Invalid runtime"

Use um dos runtimes suportados listados acima.

### Erro de permissões

Certifique-se de que as credenciais AWS têm permissão para criar todos os recursos.

```

## Estrutura do Projeto Final

```

terraform-provider-raysouz/
├── README.md # Este arquivo
├── main.go # Ponto de entrada
├── go.mod # Dependências
├── examples/ # Exemplos de uso
│ ├── simple/
│ │ └── main.tf
│ ├── with-layers/
│ │ └── main.tf
│ ├── with-vpc/
│ │ └── main.tf
│ └── complete/
│ └── main.tf
└── internal/
├── provider/
│ └── provider.go # Configuração do provider
└── raysouz/
├── client/
│ └── aws_client.go # Cliente AWS
└── resource_apigateway_lambda_routes.go # Resource principal

````

## Exemplo prático

```hcl
variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "aws_api_gateway_authorizer" {
  type    = string
  default = "koa23y"
}


terraform {
  required_version = ">= 1.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.0"
    }
    raysouz = {
      source = "raywall/raysouz"
      # version = "~> 1.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

provider "raysouz" {
  region = var.aws_region
}

data "aws_api_gateway_rest_api" "main" {
  name = "nome-da-minha-api"
}

resource "raysouz_apigateway_lambda_routes" "simple_api" {
  api_gateway_id = data.aws_api_gateway_rest_api.main.id
  stage_name     = "prod"

  lambda_config {
    function_name = "pessoas-api-handler"
    runtime       = "provided.al2"
    handler       = "bootstrap"
    zip_file      = "${path.module}/application.zip"
    memory_size   = 256
    timeout       = 30

    attached_policy_arns = [
      "arn:aws:iam::aws:policy/AmazonDynamoDBFullAccess",
    ]

    environment_variables = {
      ENVIRONMENT         = "prod"
      LOG_LEVEL           = "INFO"
      DYNAMODB_TABLE_NAME = "db_pessoas"
      DYNAMODB_HASH_KEY   = "id_pessoa"
    }
  }

  routes {
    path          = "/pessoas"
    method        = "GET"
    authorization = "CUSTOM"
    authorizer_id = var.aws_api_gateway_authorizer
  }

  routes {
    path          = "/pessoas/{pessoaId}"
    method        = "GET"
    authorization = "CUSTOM"
    authorizer_id = var.aws_api_gateway_authorizer
  }
}
````
