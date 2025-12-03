.PHONY: build-dev build-local

build-dev:
	@[ "${version}" ] || ( echo ">> please provide version=vX.Y.Z"; exit 1 )
	go build -o ~/.terraform.d/plugins/terraform-provider-raysouz_${version} .

build-local:
	@go build -o terraform-provider-raysouz .
	@chmod +x terraform-provider-raysouz