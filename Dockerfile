
# Use the official Golang image to create a build stage
FROM golang:1.22 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum files first to leverage Docker layer caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Build the Go application
RUN go build -o main .

# Use a minimal base image for production
FROM debian:bullseye-slim

# Set the working directory inside the container
WORKDIR /app

# Copy the compiled binary from the build stage
COPY --from=builder /app/main .

# Expose the application port (update if necessary)
EXPOSE 8080

# Set the default command to run the application
CMD ["./main"]