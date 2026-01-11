# One Trick Golang Backend

This server powers the One Trick Destiny 2 Application. It provides a RESTful API for the frontend application, interacting with the Bungie Destiny 2 API and storing data in Firebase.

## Project Layout

The project follows a clean architecture approach with a focus on separation of concerns:

- **`api/`**: Contains the OpenAPI specification (`openapi.json`) and generated server code (`api.gen.go`).
- **`clients/`**: Contains generated clients for external services, specifically the Bungie Destiny 2 API (`clients/bungie/`).
- **`services/`**: Contains the business logic of the application. Each service (e.g., `user`, `session`, `snapshot`) has its own directory.
- **`main.go`**: The entry point of the application.
- **`impl.go`**: Implementation of the generated server interface.
- **`generator/`**: Tools for code generation.
- **`validator/`**: Validation logic.

## Getting Started

### Prerequisites

- Go 1.21+
- Make
- Access to the Firebase project (credentials required)

### Setup

1.  **Install dependencies:**
    ```bash
    go mod tidy
    ```

2.  **Generate API code:**
    If you make changes to `api/openapi.json` or `clients/bungie/openapi.json`, run:
    ```bash
    make generate
    ```

3.  **Run Tests:**
    ```bash
    go test ./...
    ```

4.  **Run the Server:**
    ```bash
    go run .
    ```

## Development

### General Instructions

- The project uses **Firebase** as the main Database (Firestore).
- **Architecture**: API Layer -> Service Layer -> Data/DB Layer.
- **OpenAPI**:
    - One Trick API: `api/openapi.json` (Main API definition)
    - Bungie API: `clients/bungie/openapi.json` (External API definition)
    - **Note**: Most API changes should happen in `api/openapi.json`.

### Specific Instructions

- **New Endpoints**: When adding a new endpoint in `api/openapi.json`, ensure you define all basic response types (200, 400, 401, 404, 500).
- **Naming Conventions**: For schema fields like `id`, `url`, etc., add the `x-go-name` property to specify the Go struct field name (e.g., `ID`, `URL`).

## Tools & Libraries

- **[Gin](https://github.com/gin-gonic/gin)**: HTTP web framework.
- **[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)**: OpenAPI client and server code generator.
- **[Firebase Admin SDK](https://firebase.google.com/docs/admin/setup)**: For interacting with Firebase services.
- **[Zerolog](https://github.com/rs/zerolog)**: Zero allocation JSON logger.

## Contributing

1.  Create a new branch for your feature or fix.
2.  Make your changes, ensuring you follow the project's coding standards.
3.  Run `make generate` if you modified OpenAPI specs.
4.  Run tests to ensure no regressions.
5.  Submit a Pull Request.
