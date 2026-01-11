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



## Notable Libraries

- Firebase
- openapi-generator
