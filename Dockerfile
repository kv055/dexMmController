# Stage 1: Build
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o controller main.go query_scenario_logger.go

# Stage 2: Runtime - scratch (completely empty, only ~20MB binary)
FROM scratch

COPY --from=builder /build/controller /controller
COPY --from=builder /build/migrations /migrations

ENTRYPOINT ["/controller"]
