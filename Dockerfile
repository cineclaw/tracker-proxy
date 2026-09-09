# Build stage
FROM golang:alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates

ARG TARGETARCH

COPY go.mod go.sum ./
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    go mod download

COPY . .
ARG VERSION=1.0.0
ARG COMMIT=""
ARG BUILD_TIME=""
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    --mount=type=cache,id=gobuild-${TARGETARCH},target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X 'tracker-proxy/pkg/version.Version=${VERSION}' -X 'tracker-proxy/pkg/version.Commit=${COMMIT}' -X 'tracker-proxy/pkg/version.BuildTime=${BUILD_TIME}'" \
    -o /app/tracker-proxy ./cmd/server

# Final minimal stage
FROM alpine:latest

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/tracker-proxy /app/tracker-proxy

EXPOSE 9118
ENV CONFIG_PATH=/app/config.yaml

ENTRYPOINT ["/app/tracker-proxy"]
