# Junie Guidelines — oneTrick (Go Backend)

This document defines how Junie should operate when working on the oneTrick backend service. The backend is a Golang project, uses OpenAPI to generate REST endpoints, and points to a Firestore database.

Last updated: 2025-08-10 19:30 local time

## Objectives
- Make the minimal necessary changes to meet the issue requirements.
- Keep maintainers informed every step using the required `<UPDATE>` format.
- Prefer small, safe, and reversible edits.

## Required Response Format
Every Junie message must have TWO parts in this exact order:
1) `<UPDATE>` block with:
   - `<PREVIOUS_STEP>`: Brief summary of outcomes since the last step.
   - `<PLAN>`: Numbered list; include progress marks at the end of each line: ✓ (done), * (in progress), ! (failed). Keep sub-points concise and marked as well.
   - `<NEXT_STEP>`: The immediate next action.
2) Exactly one tool call that corresponds to `<NEXT_STEP>`.

Never call a tool before the `<UPDATE>` block. Never omit the tool call.

## Tools and Execution Rules
- Use the provided tools (search_project, get_file_structure, open, create, search_replace, run_test, build, etc.).
- Do not mix terminal commands with special tools in the same step.
- Do not create files outside this repository.
- When searching, prefer search_project over shell commands for discovery.
- When editing files, prefer search_replace with exact unique blocks.
- Use create only to add new files; re-run create to overwrite only files you created in the current session.
- Use run_test for tests and build to compile when necessary.
- If unclear, use ask_user to request clarification.

## Development Workflow
1. Understand the issue and craft an initial plan in `<PLAN>`. Use concise numbered points with progress marks. Include sub-points if needed.
2. Explore the codebase:
   - Use search_project to locate relevant files, symbols, and text.
   - Use get_file_structure and open to inspect target areas.
3. Reproduce issues (if applicable) with a minimal script or by running targeted tests.
4. Implement minimal changes:
   - Keep diffs small and focused on the issue.
   - Follow project conventions (Go module layout, code generation boundaries).
5. Verify:
   - Re-run reproduction steps/tests.
   - Ensure no related tests regress.
6. Report:
   - Summarize changes and outcomes in `<UPDATE>`.
   - If complete, mark plan items ✓ and proceed to submit when asked.

## Backend-Specific Conventions (Go + OpenAPI + Firestore)
- Project Type: Go modules (Golang) with OpenAPI-driven REST endpoints.
- Code Generation:
  - Do not hand-edit generated files. Limit edits to non-generated packages.
  - Keep OpenAPI spec (e.g., openapi.yaml or openapi.yml) as the single source of truth for API shapes.
  - When updating API contracts:
    - Modify the OpenAPI spec.
    - Re-run code generation using the project’s standard script or Makefile target (e.g., `make generate` or `go generate ./...`).
    - Commit both the spec and regenerated code together.
- Generation Tools:
  - If the project uses `oapi-codegen` (common in Go):
    - Respect existing config files (e.g., `oapi-codegen.yaml`).
    - Keep package names and output paths consistent with current structure.
  - If another generator is used, follow the existing scripts and config.
- Firestore Usage:
  - Prefer the Firestore emulator for local development and tests when available.
  - Keep production credentials out of the repo. Use environment variables or configured secret managers.
  - Document required env vars (examples):
    - `GOOGLE_CLOUD_PROJECT`
    - `FIRESTORE_EMULATOR_HOST` (for local dev)
    - Any service account key path variables if the emulator is not used in CI.
  - Ensure queries are indexed as needed; keep index configuration under version control if the project does so (e.g., firestore.indexes.json).
- Error Handling & Logging:
  - Return typed errors wrapped with context.
  - Ensure REST handlers translate domain errors to appropriate HTTP status codes.
  - Use structured logging where available; redact sensitive data.
- Testing:
  - Unit tests alongside packages: `*_test.go`.
  - Use emulator for integration tests when feasible.
  - Keep CI-friendly test targets; avoid external stateful dependencies without fakes/mocks.

## Build, Run, and Generate (Typical Patterns)
- Build: `go build ./...`
- Tests: `go test ./...`
- Lint/Format: `gofmt -s -w .` and any configured linters (e.g., `golangci-lint run`).
- OpenAPI Codegen (examples; adapt to project):
  - `make generate`
  - or `go generate ./...`
  - or `oapi-codegen -config oapi-codegen.yaml openapi.yaml`
- Firestore Emulator (examples; adapt to project):
  - Start via local tooling (e.g., `gcloud beta emulators firestore start` or Docker wrapper).
  - Export `FIRESTORE_EMULATOR_HOST=localhost:8080` (or project’s chosen port).

## Commit and Change Scope
- One issue = minimal set of changes.
- Prefer additive changes over refactors unless necessary for the fix.
- Avoid speculative optimizations.
- Do not break API contracts without updating the OpenAPI spec and regenerating the code.

## Communication Standards
- Be transparent about findings, blockers, and edge cases in `<PREVIOUS_STEP>`.
- Keep `<NEXT_STEP>` focused on a single concrete action.
- If the requirements appear to conflict, confirm with ask_user.

## Repository-Specific Notes (oneTrick backend)
- Language: Go
- API: OpenAPI-driven (generated server stubs/clients)
- Database: Firestore (use emulator locally when possible)
- Never edit generated files directly; update the spec and re-generate.
- Keep environment setup steps documented near scripts (Makefile or scripts/).

## Safety
- Back up via minimal, reversible edits.
- Use search_replace with precise unique blocks to avoid unintended changes.
- When unsure, pause and ask for guidance.
