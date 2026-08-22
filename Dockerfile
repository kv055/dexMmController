# Stage 1: Build
FROM golang:1.25-alpine AS builder
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache git build-base

WORKDIR /build/dexMmController

COPY commons/ ../commons/
COPY dexMmController/go.mod dexMmController/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    sh -ec 'for attempt in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download attempt $attempt failed, retrying..."; sleep $((attempt * 10)); done; exit 1'

COPY dexMmController/main.go dexMmController/main_lnd_backup.go dexMmController/main_lnd_provision.go dexMmController/boltz_runtime.go ./
COPY dexMmController/internal/ ./internal/

RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
	-ldflags="-linkmode external -extldflags -static -w -s" \
    -o controller .

# Stage 2: Runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates wget

COPY --from=builder /build/dexMmController/controller /controller

ENTRYPOINT ["/controller"]
