

run:
	go run .

build:
	go build -o server .

emulator:
	docker compose up -d firestore-emulator

emulator-down:
	docker compose down

emulator-logs:
	docker compose logs -f firestore-emulator

sync-db:
	go run ./scripts/sync_db/main.go

sync-app-db:
	go run ./scripts/sync_db/main.go -skip-manifest

count-prod-db:
	go run ./scripts/count_prod_docs/main.go

generate:
	npx @redocly/cli@latest bundle ./openapi/openapi.yaml  --output openapi.yaml
	go generate ./...

split-endpoints:
	npx @redocly/cli@latest  split ./api/openapi.yaml --outDir=openapi

#lint-swagger:
#	make generate
#	npx @redocly/cli@latest  lint --extends minimal ./openapi.yaml
#
#gen-swagger:
#	make generate
#	npx @redocly/cli@latest  build-docs ./openapi.yaml

