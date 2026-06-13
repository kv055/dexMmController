# Stage 1: Build
FROM golang:1.23-alpine AS builder
ARG GOPROXY=https://goproxy.io,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o controller .

# Stage 2: Runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates wget

COPY --from=builder /build/controller /controller

ENTRYPOINT ["/controller"]
