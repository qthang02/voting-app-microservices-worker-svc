# ---- Build Stage ----
FROM golang:1.22-alpine AS build

WORKDIR /src

# Download dependencies first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Build the binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/worker .

# ---- Runtime Stage ----
FROM alpine:3.19

# Add wget for healthcheck (smaller than curl on alpine)
RUN apk --no-cache add ca-certificates wget

WORKDIR /app
COPY --from=build /app/worker .

EXPOSE 8080

ENTRYPOINT ["./worker"]
