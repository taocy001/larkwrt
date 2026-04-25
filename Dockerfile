# syntax=docker/dockerfile:1.7
# ──────────────────────────────────────────────────────────────────────────────
# Stage 1 — dependency cache
# Download modules separately so this layer is cached across code changes.
# ──────────────────────────────────────────────────────────────────────────────
FROM golang:1.21-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# ──────────────────────────────────────────────────────────────────────────────
# Stage 2 — test
# Run unit + integration tests inside the container (Linux /proc is real).
# ──────────────────────────────────────────────────────────────────────────────
FROM deps AS test
WORKDIR /src
COPY . .
# -count=1 disables test caching so we always get fresh results
# -timeout 120s covers integration tests with WS reconnect delays
CMD ["go", "test", \
     "-v", \
     "-count=1", \
     "-timeout=120s", \
     "-coverprofile=/tmp/coverage.out", \
     "-covermode=atomic", \
     "./..."]

# ──────────────────────────────────────────────────────────────────────────────
# Stage 3 — cross-compile all architectures
# ──────────────────────────────────────────────────────────────────────────────
FROM deps AS builder
WORKDIR /src
COPY . .

ARG VERSION=dev
ARG LDFLAGS="-s -w -X main.version=${VERSION}"

RUN mkdir -p /dist

# amd64 (x86 soft-router / VM)
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" \
    -o /dist/larkwrt-agent-amd64 ./cmd/agent

# arm64 (GL.iNet MT6000, RPi 4/5 64-bit)
RUN GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" \
    -o /dist/larkwrt-agent-arm64 ./cmd/agent

# armv7 hard-float (GL.iNet MT3000/MT1300, RPi 32-bit)
RUN GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" \
    -o /dist/larkwrt-agent-arm ./cmd/agent

# MIPS big-endian softfloat (TP-Link WR, Archer series)
RUN GOOS=linux GOARCH=mips GOMIPS=softfloat CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" \
    -o /dist/larkwrt-agent-mips ./cmd/agent

# MIPS little-endian softfloat (MT7620, RT-N56U)
RUN GOOS=linux GOARCH=mipsle GOMIPS=softfloat CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" \
    -o /dist/larkwrt-agent-mipsle ./cmd/agent

RUN ls -lh /dist/

# ──────────────────────────────────────────────────────────────────────────────
# Stage 4 — artifact export image (used with `docker build --output`)
# ──────────────────────────────────────────────────────────────────────────────
FROM scratch AS artifacts
COPY --from=builder /dist/ /
