# Resource: raysouz_apigateway_lambda_routes

Cria uma função Lambda com rotas no API Gateway existente.

## Example Usage

```hcl
resource "raysouz_apigateway_lambda_routes" "example" {
  api_gateway_id = "abc123"

  lambda_config {
    function_name = "my-api-handler"
    runtime       = "python3.11"
    handler       = "index.handler"
    zip_file      = "lambda.zip"
    memory_size   = 256
    timeout       = 30
  }

  routes = [
    {
      path   = "/users"
      method = "GET"
    }
  ]
}
```
