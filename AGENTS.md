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
  - **Domain != Game-Agnostic**: Destiny 2 concepts (`Loadout`, `Snapshot`, `CompRank`, `GameMode`, inventory slots) *are* One Trick's core business domain; Bungie's REST wire format (`bungie.*`, component bitmasks like `205/300/305`, nullable `*uint32` manifest pointers, OAuth token headers) is external adapter noise.
  - **Hexagon Core Taxonomy (`core/*`)** — **zero imports** of `oneTrick/api`, `oneTrick/adapters/*`, `oneTrick/clients/*`, `cloud.google.com/go/firestore`, `gin`, or legacy `services/*`:
    - **Shared Pure Domain Models (`core/domain`, `core/gamemode`, `core/inventory`)**: Plain, value-semantic Go structs (`[]T` over `[]*T`, useful zero values, no `json:`/`firestore:` tags, no nullable OpenAPI pointer soup) for cross-cutting nouns (`User`, `Session`, `Snapshot`, `Loadout`, `Aggregate`) plus pure invariant methods/functions (`Loadout.Hash()`, `ComputeSummary()`, KD/stat math, merge compatibility, state transitions). Keep feature-specific composite read-models/projections (`MatchReport`, `SessionDetail`, `FireteamOverview`) local to their `core/<feature>` package to avoid half-hydrated "God structs". Shared model packages have **zero ports, zero I/O, and zero imports of `core/<feature>` service packages**.
    - **Flat Peer Domain `Service`s (`core/user`, `core/snapshot`, `core/aggregate`, `core/session`, `core/activity`, `core/stats`)**: Own single-domain orchestration, authorization/ownership rules (`ErrUnauthorized`), and sentinel errors (`ErrNotFound`). **Hard rule:** *A Domain `Service` never calls another Domain `Service`*. Before coupling two domains, apply the **3-Step Escalation Ladder**:
      1. **Plain cross-collection read/write (no business invariants)**: Declare a narrow consumer Store/Reader port (`UserReader`, `AggregateReader`) in the consuming package and wire the permanent `firestore.*Store` (or cache decorator) directly in `main.go` — never build dumb CRUD pass-through `Service` wrappers.
      2. **Shared pure business rules/math**: Hoist into pure functions/methods in `core/domain` so peer `Service`s share logic with zero service hierarchy.
      3. **Stateful cross-domain orchestration (I/O + invariants across multiple domains)**: Promote into an explicit **`Workflow` / `Orchestrator`** rather than chaining `Service -> Service`.
    - **Cross-Domain `Workflow`s / `Orchestrator`s (`TickWorkflow`, `LoginWorkflow`, `MaintenanceWorkflow`)**: Explicit coordinator types inside `core/` that orchestrate multi-domain jobs across peer Domain `Service`s (via narrow 1–3 method consumer interfaces like `SnapshotTracker`) and Driven Ports. Max call depth inside `core/` is strictly **2** (`Workflow -> Domain Service`), forming a one-way DAG with zero cycles.
  - **Narrow, Consumer-Defined Ports (`core/<feature>`)**:
    - Ports are small (1–6 method) capability-named wishlists (`LiveLoadoutReader`, `CarnageReportGateway`, `AggregateReader` — never vendor-named mega-ports like `BungiePort`) declared inside the consuming `core/*` package. Read-only external APIs still cross ports.
  - **Driving Adapters (`adapters/driving/http`, `impl.go`, `scripts/*`, cron triggers)** — *drive the core*:
    - Thin entrypoints outside the wall: enrich `clog` `"actor"`/`"request"` fields, validate transport framing, convert `api.*` request DTOs -> core inputs, call **one** `Service` or `Workflow` method, map domain sentinel errors -> OpenAPI response structs (`200`/`204`, `400`, `401`, `403`, `404`, `500`), and convert core results -> `api.*` response DTOs in `adapters/driving/http`.
    - Never put multi-step business loops, cross-save/account provisioning, or raw persistence map mutations inside HTTP handlers (`impl.go`) or admin adapters — keep business workflows in `core/` so HTTP handlers, cron jobs, and `scripts/*` CLI tools can drive the exact same `Service` / `Workflow`.
  - **Driven Adapters (`adapters/driven/{firestore,bungie,cache}`)** — *permanent I/O & translation implementations driven by the core*:
    - **Permanent Adapters vs. Temporary Bridges**: `adapters/driven/{firestore,bungie,cache}` are permanent architectural layers (an Adapter is anything touching external SDKs/I/O like `*firestore.Client` or `*bungie.ClientWithResponses`). Only legacy `services/*` wrappers (e.g., `adapters/driven/activity`) are temporary migration bridges and must carry a removal checklist header.
    - **Per-Collection Firestore Stores (`adapters/driven/firestore`)**: Keep separate per-collection Store structs (`SessionStore`, `SnapshotStore`, `AggregateStore`, `UserStore`) owning their own `*Doc` serialization and `codes.NotFound -> ErrNotFound` translation. Because cross-cutting domain structs live in `core/domain`, one concrete `*Store` struct can implicitly satisfy narrow ports across multiple `core/*` packages without wrapper boilerplate.
    - **Bungie Anti-Corruption Layer (`adapters/driven/bungie`) — "Vendor Quirk vs. Product Policy"**: Confine `bungie.*` vendor structs, component bitmasks, mid-request OAuth token refresh plumbing, `ErrorCode: 1665` / throttle mapping, `OverrideStyleItemHash` fallbacks, and default Manifest hash hydration strictly inside the Bungie/cache adapters. Promote `ManifestLookup` to a separate Core port *only* when Core requires cross-port orchestration (e.g., skipping lookups when Firestore `Aggregate` data already exists, or applying product-level partial-failure degradation).
  - **Composition Root (`main.go`)**:
    - Instantiates `*firestore.Client`, HTTP/Bungie clients, and caches once, constructs permanent driven `Store`/`Gateway` adapters, injects them into peer Domain `Service`s, injects `Service`s + `Gateway`s into cross-domain `Workflow`s, and wires `Service`s/`Workflow`s into `Server`. Keep `main.go` free of adapter structs and translation logic.
  - **Incremental / Boy-Scout Migration Rule**:
    - **New endpoints, services, workflows, and domains** belong in `core/*` (`core/domain` + `core/<feature>`) with driven adapters under `adapters/driven/*` and driving API converters/handlers under `adapters/driving/http` (injected from `main.go`).
    - **Touched slices in legacy `services/*` packages** must avoid introducing new `api.*` / `bungie.*` / `map[string]any` leaks or raw `s.DB.Collection(...)` calls in services—extract/extend the package's driven `store` port for the touched path without forcing unrelated big-bang rewrites.
- This project has two OpenAPI Schemas:
  - **One Trick API**: Split source specs live in `openapi/` (`openapi/openapi.yaml`, `openapi/paths/*`, `openapi/components/*`), which `make generate` bundles into `./openapi.yaml` and generates into `api/api.gen.go`. Never hand-edit `openapi.yaml` or `api/api.gen.go`.
  - **Bungie Destiny 2 API**: `clients/bungie/openapi.json` (an unmodified upstream vendor spec — **never edit `clients/bungie/openapi.json` directly**; all Bungie type coercions, path fixes, and `x-go-*` / `x-enum-varnames` customizations must be made in `clients/bungie/overlay.yaml`).
  - Most feature changes take place in the One Trick OpenAPI schema (`openapi/`), not Bungie's.

## Specific Instructions for this Project

### API-Specific Information (Driving Adapter)

- When creating a new endpoint in `openapi/paths/*.yaml` (and referencing it in `openapi/openapi.yaml`), include all standard response types (`200`/`201`/`204`, `400`, `401`, `404`, `500`, etc.) and run `make generate`.
- Any fields in schemas ending with `id`, `url`, etc., must specify `x-go-name` (`ID`, `URL`, `UserID`, `SnapshotID`, etc.) for idiomatic Go struct field names.
- Keep `api.*` generated types confined to the HTTP adapter (`impl.go`) and `adapters/driving/http` converters for new/refactored features rather than passing pointer-heavy OpenAPI DTOs deep into core logic or directly into `ref.Set(...)`. Place HTTP request/response DTO mapping functions in `adapters/driving/http` rather than growing `impl.go`.

### Shared Internal Packages (`set/`, `utils/`, `adapters/driving/http`)

- Always prefer the repo's shared packages over hand-rolling boilerplate for common patterns:
  - **`oneTrick/set`**: Use `set.New[T]()` / `set.FromSlice` for membership sets and `set.NewOrdered[T]()` / `set.OrderedFromSlice` when deduplicating while preserving insertion order (instead of manual `map[T]struct{}` + slice loops).
  - **`oneTrick/utils`**: Reuse collection and utility helpers (`utils.UniqueNonZero`, `utils.UnionNonZero`, `utils.UniqueTrimmedStrings`, `utils.Chunk`, hashing, etc.) before writing ad-hoc slice deduplication or chunking logic.
  - **`oneTrick/adapters/driving/http`**: Place `api.*` <-> `core/*` DTO conversion helpers here to keep `impl.go` handlers thin.

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

## Issue & Project Tracking (YouTrack)

- `Shannor/OneTrick`, `Shannor/OneTrickUI`, and `Shannor/OneTrick-Scripts` track all work in **YouTrack** under the **One Trick (`OT`)** Kanban board (`https://gruntt.youtrack.cloud/agiles/218-1/current`) — do **not** create new GitHub Issues.
- Use the `youtrack` CLI (`/opt/homebrew/bin/youtrack`, see the `youtrack-cli` skill) to read, create, and transition tickets:
  - Set `Subsystem` to `Backend` (for `OneTrick`), `UI` (for `OneTrickUI`), `Full-Stack` (cross-repo), or `Scripts` (for `OneTrick-Scripts`).
  - Set initial `Stage` to `Backlog` (or `Ready` when actionable): `youtrack issue create --project OT --summary "..." --custom-fields '{"Stage":"Backlog","Subsystem":"Backend"}' --description-file -`.

### Branching, Commits & Automatic Stage Sync

- **Branch naming**: Always include the YouTrack ticket ID in branches using `<type>/ot-<id>-<short-slug>` (or `<type>/ot-<id>`), where `<type>` is `feat`, `fix`, `refactor`, `perf`, `chore`, or `docs` (e.g., `feat/ot-12-fireteam-overview`, `fix/ot-16-mid-session-comp`).
- **Pull Request titles**: Prefix every PR title with `[OT-<id>]: ` so the ticket is immediately scannable and YouTrack attaches PR status events automatically (e.g., `[OT-54]: Add fireteam overview`).
- **Commits**: Include `OT-<id>` in commit subjects (e.g., `feat(session): ... (OT-12)`) so YouTrack's GitHub VCS integration attaches commits to the ticket.
- **Automated Kanban transitions** (via the `One Trick GitHub Stage Sync` workflow attached to project `OT`):
  - First pushed commit referencing `OT-<id>` moves `Backlog` / `Ready` -> **`In Progress`**.
  - Opening a PR referencing `OT-<id>` moves `Stage` -> **`In Review`** (unless already `Done`).
  - Merging the PR moves `Stage` -> **`Done`** (closing/declining an `In Review` PR moves it back to **`In Progress`**).

## Notable Libraries

- Firebase
- openapi-generator
- canonical-logger (`clog`)
