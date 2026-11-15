# Stage 1: Build
FROM golang:1.27-alpine AS builder
ARG GOPROXY=https://goproxy.io,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /build/dexMmController

COPY commons/ ../commons/
COPY dexMmController/go.mod dexMmController/go.sum ./
RUN go mod download

COPY dexMmController/main.go dexMmController/main_lnd_backup.go ./
COPY dexMmController/internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o controller .

# Stage 2: Runtime
FROM alpine:3.24

RUN apk add --no-cache ca-certificates wget

COPY --from=builder /build/dexMmController/controller /controller

ENTRYPOINT ["/controller"]
