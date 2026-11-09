# Project: One Trick Golang Backend

This server powers the One Trick Destiny 2 Application 

## Setup commands

- Install dependencies: `go mod tidy`
- Run tests: `go test ./...`
- Run api generation: `make generate`

## General Instructions

- Project use Firebase as the main Database.
- The project uses a traditional approach of API Layer, Service Layer, and Data/DB Layer.
- The project uses a clean architecture approach with a focus on separation of concerns and testability.
- This project has two Open API Schemas.
  - One is for One Trick which is in `api/openapi.json`
  - The other is for generating from Bungie's Destiny API which is in `clients/bungie/openapi.json`
  - Most changes will need to take place in the One Trick Open API schema and not in bungie's.
- Each Service can be found in the `/services` folder and provides the business logic for the application.
  

## Specific Instructions for this Project

- When creating a new endpoint in the open api spec in `api` folder and file `openapi.json`, add all basic response types, 200, 400, 401, 404, 500, etc.
- Any fields in schemas with `id`, `url`, etc, should add the `x-go-name` property to specify the Go struct field name. To `ID`, `URL`, etc.

### API-Specific information

## DB Specific Information

### DAO Testing 



## Logging Conventions (`canonical-logger` / `clog`)

- The project uses [`github.com/Shannor/canonical-logger`](https://github.com/Shannor/canonical-logger) (`clog`) for canonical wide-event logging (one structured log line per HTTP request or background task).
- **Process lifecycle events** (startup/shutdown in `main.go`, `clients/gcp`, `envvars`) use standard `slog.Info` / `slog.Error`.
- **Inside active requests or background tasks** (`impl.go`, `services/*`):
  - Never call `slog.Info`, `slog.Warn`, `slog.Error`, or `slog.Debug`.
  - Enrich the context's `FieldBag` via `clog.WithGroupField` / `clog.WithGroupFields`.
  - API handlers (`impl.go`) enrich the `"request"` and `"actor"` groups; outcomes are recorded automatically by `logger.GinStrictMiddleware`.
  - Services (`services/*`) enrich `"<serviceName>.<methodName>"` groups and return standard Go errors (never write to the `"error"` group directly).
  - Use `clog.Go` for detached background goroutines and `clog.LoopRollup` for batch loops.
  - All `clog.Fields` keys must be `camelCase` with the `Id` suffix (e.g., `"userId"`, `"sessionId"`, `"snapshotId"`, `"characterId"`). Never log PII.

## Notable Libraries

- Firebase
- openapi-generator
- canonical-logger (`clog`)
