build-dev:
	@[ "${version}" ] || ( echo ">> please provide version=vX.Y.Z"; exit 1 )
	go build -o ~/.terraform.d/plugins/terraform-provider-raysouz_${version} .

.PHONY: build-dev
