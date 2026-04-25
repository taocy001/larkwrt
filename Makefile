BINARY  := larkwrt-agent
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD   := go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all deps build-all mips mipsle arm arm64 amd64 clean install test coverage

all: deps build-all

deps:
	go mod download
	go mod verify

dist:
	mkdir -p dist

# ── Cross-compile targets ──────────────────────────────────────────────────────
# MIPS big-endian softfloat (TP-Link, 部分旧款路由)
mips: dist
	GOOS=linux GOARCH=mips GOMIPS=softfloat CGO_ENABLED=0 \
		$(BUILD) -o dist/$(BINARY)-mips ./cmd/agent

# MIPS little-endian softfloat (RT-N56U 等)
mipsle: dist
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat CGO_ENABLED=0 \
		$(BUILD) -o dist/$(BINARY)-mipsle ./cmd/agent

# ARM v7 硬浮点 (GL.iNet MT1300/MT3000, Raspberry Pi 32-bit)
arm: dist
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
		$(BUILD) -o dist/$(BINARY)-arm ./cmd/agent

# ARM64 (GL.iNet MT6000, Raspberry Pi 64-bit)
arm64: dist
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		$(BUILD) -o dist/$(BINARY)-arm64 ./cmd/agent

# x86-64 (x86 OpenWrt VM / 软路由)
amd64: dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		$(BUILD) -o dist/$(BINARY)-amd64 ./cmd/agent

build-all: mips mipsle arm arm64 amd64

# 本机调试构建
dev: dist
	CGO_ENABLED=0 go build -o dist/$(BINARY)-dev ./cmd/agent

# 安装到本机（调试用）
install: dev
	sudo install -m 755 dist/$(BINARY)-dev /usr/local/bin/$(BINARY)

clean:
	rm -rf dist/

test:
	go test -v -race -count=1 -timeout=120s ./...

coverage:
	go test -count=1 -timeout=120s -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out

# 生成 go.sum（首次）
tidy:
	go mod tidy
