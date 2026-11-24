

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

deploy-indexes:
	npx --yes firebase-tools deploy --only firestore:indexes --project gruntt-destiny --non-interactive

monitor-logs:
	go run ./scripts/monitor_logs/main.go $(ARGS)

list-servers:
	@go run ./scripts/list_servers/main.go $(ARGS)

BUNGIE_SPEC_REF ?= master

sync-bungie-spec:
	curl -fsSL https://raw.githubusercontent.com/Bungie-net/api/$(BUNGIE_SPEC_REF)/openapi.json -o ./clients/bungie/openapi.json

generate:
	npx --yes @redocly/cli@latest bundle ./openapi/openapi.yaml --output openapi.yaml
	go generate ./...

split-endpoints:
	npx --yes @redocly/cli@latest split ./api/openapi.yaml --outDir=openapi

#lint-swagger:
#	make generate
#	npx @redocly/cli@latest  lint --extends minimal ./openapi.yaml
#
#gen-swagger:
#	make generate
#	npx @redocly/cli@latest  build-docs ./openapi.yaml

