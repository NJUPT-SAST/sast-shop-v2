# syntax=docker/dockerfile:1

# Build any service image with:
#   docker build --build-arg SERVICE=<userservice|catalogservice|paymentservice|spotservice|errandservice> .

# ---- Stage 1: proto code generation (buf remote plugins need network) ----
# Based on the same golang image as the build stage and installs buf via
# go install: org images like bufbuild/buf are frequently 403'd by Chinese
# Docker mirrors, while library/golang is reliably mirrored.
FROM golang:1.26 AS gen
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=$GOPROXY \
    GOSUMDB=$GOSUMDB
WORKDIR /src
# Retry once: sumdb lookups through China mirror proxies intermittently 504.
# Chinese mirrors: pass --build-arg GOPROXY=https://goproxy.cn,direct
# (and GOSUMDB=off if the mirror's sumdb endpoint keeps failing).
RUN --mount=type=cache,target=/go/pkg/mod \
    go install github.com/bufbuild/buf/cmd/buf@v1.54.0 || \
    (sleep 5 && go install github.com/bufbuild/buf/cmd/buf@v1.54.0)
COPY buf.yaml buf.gen.yaml buf.lock ./
COPY proto/ proto/
# Writes gen/protocolbuffers/go + gen/connectrpc/go (no go.mod files; created below).
RUN buf generate

# ---- Stage 2: build (recreates the gitignored workspace from scratch) ----
FROM golang:1.26 AS build
ARG SERVICE
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=$GOPROXY \
    GOSUMDB=$GOSUMDB \
    CGO_ENABLED=0

WORKDIR /src

# Fail fast on a typo'd --build-arg SERVICE.
RUN case "$SERVICE" in \
      userservice|catalogservice|paymentservice|spotservice|errandservice) ;; \
      *) echo "unknown SERVICE: $SERVICE" >&2; exit 1 ;; \
    esac

COPY --from=gen /src/gen ./gen
COPY internal/ internal/

# go.work, go.work.sum and gen/ are gitignored and absent from a clean
# checkout: recreate exactly what `make proto` does, for the target service.
RUN test -f gen/protocolbuffers/go/go.mod || \
      (cd gen/protocolbuffers/go && go mod init buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go) \
 && test -f gen/connectrpc/go/go.mod || \
      (cd gen/connectrpc/go && go mod init buf.build/gen/go/sast/sast-shop-v2/connectrpc/go) \
 && go work init \
 && go work use ./internal/pkg ./internal/service/${SERVICE} ./gen/protocolbuffers/go ./gen/connectrpc/go

# Build from the repo root in workspace mode. Never cd into the service dir:
# userservice/catalogservice resolve internal/pkg only through go.work.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/app ./internal/service/${SERVICE}/cmd/app

# ---- Stage 3: runtime ----
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app
COPY --from=build /out/app /usr/local/bin/app
USER app
WORKDIR /app
# EXPOSE intentionally omitted: the server compose uses network_mode: host; in
# bridge mode the compose file maps ports (metadata only anyway).
ENTRYPOINT ["/usr/local/bin/app"]
