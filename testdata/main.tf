provider "raysouz" { }

resource "raysouz_custom_resource" "example" {
  message = "Hello, Test!"
  cloud   = "azure"  # Substitua por "azure" ou "gcp" conforme necessário
}

# Usando dynamic para personalizar conteúdo com base na nuvem
# resource "dynamic" "example_resources" {
#   for_each = toset(["aws", "azure", "gcp"])

#   content {
#     cloud = each.key

#     # Adicione aqui os recursos específicos para cada nuvem
#     # Exemplo: resource "aws_instance" para AWS, resource "azurerm_virtual_machine" para Azure, etc.
#     resource_type = "aws_instance"
#     resource_name = "example_instance_${each.key}"

#     # Adapte conforme necessário para a configuração específica do recurso na nuvem
#     ami           = "ami-12345678"
#     instance_type = "t2.micro"
#   }
# }
