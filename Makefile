

generate:
	#npx @redocly/cli@latest bundle ./openapi/split.openapi.yaml  --output openapi.yaml
	go generate ./...

#split-endpoints:
#	npx @redocly/cli@latest  split ./openapi/split.openapi.yaml --outDir=openapi
#
#lint-swagger:
#	make generate
#	npx @redocly/cli@latest  lint --extends minimal ./openapi.yaml
#
#gen-swagger:
#	make generate
#	npx @redocly/cli@latest  build-docs ./openapi.yaml

