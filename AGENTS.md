# Project: One Trick Golang Backend

This server powers the One Trick Destiny 2 Application 

## Setup commands

- Install dependencies: `go mod tidy`
- Run tests: `go test ./...`
- Run api generation: `make generate`
- Sync upstream Bungie OpenAPI spec: `make sync-bungie-spec` (optionally override `BUNGIE_SPEC_REF=<commit-or-tag>`, defaults to `master`)
- Monitor production GCP logs (errors & slow calls): `make monitor-logs` (writes reports to `reports/gcp-log-monitor-latest.md` and `.json`)
- List running local `oneTrick` servers across git worktrees and ports: `make list-servers` (supports `ARGS="-all"`, `ARGS="-v"`, `ARGS="-json"`)

## General Instructions & Architecture (Pragmatic Hexagonal / Ports & Adapters)

- Project uses Google Cloud Firestore as the main database.
- The codebase follows **Pragmatic Go Hexagonal Architecture (Ports & Adapters)** (migrating incrementally from an older `API -> Service -> DB` split in `services/*` where `api.*` DTOs and `*firestore.Client` leaked into services):
  - **Hexagon Core — Application Services & Domain Models (`/core/<domain>`, e.g. `core/fireteam`)**:
    - Pure business orchestration, authorization/ownership rules, and cross-port workflows with **zero imports** of `oneTrick/api`, `oneTrick/clients/bungie`, `cloud.google.com/go/firestore`, `gin`, or legacy `services/*`.
    - Domain models are plain, value-semantic Go structs (`[]T` over `[]*T`, useful zero values, no `json:`/`firestore:` tags, no nullable OpenAPI pointer soup) with pure helper functions/methods only for cohesive domain invariants (e.g., loadout hashing, base identity keys, KD/stat math, merge compatibility, orphan detection) — avoid Java DDD "rich entity" ceremony.
    - Core packages define **domain sentinel errors** (`ErrNotFound`, `ErrUnauthorized`, etc.) rather than returning HTTP status codes or gRPC/Firestore status codes.
    - Core packages declare **narrow, consumer-defined driven ports** (store/repository interfaces and external gateway interfaces) speaking domain types rather than depending on `*firestore.Client`, `map[string]any` mutation callbacks, or 12-method sibling `Service` interfaces.
  - **Driving Adapters (`impl.go` / HTTP handlers, `scripts/*`, cron ticks)**:
    - HTTP handlers stay thin: enrich `clog` `"actor"`/`"request"` fields, validate transport inputs, convert `api.*` request DTOs -> domain inputs, call **one** Application Service method, map domain sentinel errors -> OpenAPI response structs (`200`/`204`, `400`, `401`, `404`, `500`), and convert domain results -> `api.*` response DTOs.
    - Never put multi-step business loops or raw persistence map mutations inside HTTP handlers.
  - **Driven Adapters (`/adapters/<domain>`, legacy `services/*/store.go`, `clients/bungie` gateway, caches)**:
    - Implement `core/<domain>` driven ports, own Firestore document serialization/queries and `codes.NotFound -> ErrNotFound` translation, and confine generated `bungie.*` vendor structs and legacy `services/*` bridging strictly inside the adapter boundary.
  - **Composition Root (`main.go`)**:
    - Instantiates `*firestore.Client`, HTTP/Bungie clients, and caches once, constructs driven adapters, injects them into Application Services, and wires Application Services into `Server`. Keep `main.go` free of adapter structs and translation logic.
  - **Incremental / Boy-Scout Migration Rule**:
    - **New endpoints, services, and domains** belong in `core/<domain>` + `adapters/<domain>` following strict Hexagonal boundaries (pure domain structs, consumer-defined driven ports injected from `main.go`, explicit DTO converters).
    - **Touched slices in legacy `services/*` packages** must avoid introducing new `api.*` / `bungie.*` / `map[string]any` leaks or raw `s.DB.Collection(...)` calls in services—extract/extend the package's driven `store` port for the touched path without forcing unrelated big-bang rewrites.
- This project has two OpenAPI Schemas:
  - **One Trick API**: Split source specs live in `openapi/` (`openapi/openapi.yaml`, `openapi/paths/*`, `openapi/components/*`), which `make generate` bundles into `./openapi.yaml` and generates into `api/api.gen.go`. Never hand-edit `openapi.yaml` or `api/api.gen.go`.
  - **Bungie Destiny 2 API**: `clients/bungie/openapi.json` (an unmodified upstream vendor spec — **never edit `clients/bungie/openapi.json` directly**; all Bungie type coercions, path fixes, and `x-go-*` / `x-enum-varnames` customizations must be made in `clients/bungie/overlay.yaml`).
  - Most feature changes take place in the One Trick OpenAPI schema (`openapi/`), not Bungie's.

## Specific Instructions for this Project

### API-Specific Information (Driving Adapter)

- When creating a new endpoint in `openapi/paths/*.yaml` (and referencing it in `openapi/openapi.yaml`), include all standard response types (`200`/`201`/`204`, `400`, `401`, `404`, `500`, etc.) and run `make generate`.
- Any fields in schemas ending with `id`, `url`, etc., must specify `x-go-name` (`ID`, `URL`, `UserID`, `SnapshotID`, etc.) for idiomatic Go struct field names.
- Keep `api.*` generated types confined to the HTTP adapter and converter boundary for new/refactored features rather than passing pointer-heavy OpenAPI DTOs deep into service math or directly into `ref.Set(...)`.

## DB & Driven Adapter Information

- Firestore queries, `firestore.Update` slices, document structs (`*Doc` with `firestore:"..."` tags), and gRPC status inspection belong strictly inside driven store adapters (`store.go`), never in `impl.go` or core service logic.
- Respect collection ownership: services should read/mutate another domain's data through a narrow consumer port rather than querying sibling Firestore collections (`"aggregates"`, `"snapshots"`, `"users"`) directly.
- Verify composite index requirements (`firestore.indexes.json` / GCP indexes) whenever introducing new multi-field `Where` + `OrderBy` queries.

### Store & Service Testing

- **Service & Domain Unit Tests (`*_test.go`)**: Default to fast, deterministic unit tests using lightweight in-memory fakes/stubs implementing the service's driven `store` / `gateway` ports (no live Firestore emulator or Bungie network calls needed).
- **Driven Store Integration Tests**: When testing complex Firestore queries, filters, subcollection cleanups, or serialization in `store.go`, test against the local Firestore emulator (`make emulator`, `FIRESTORE_EMULATOR_HOST`). 



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
