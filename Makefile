GIT_VERSION = $(shell git rev-parse --short HEAD)
VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev-$(GIT_VERSION)")
BUILD_TIME = $(shell date -u '+%Y-%m-%d_%H:%M:%S')

.PHONY: build
build:
	CGO_ENABLED=0 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent main.go

.PHONY: build-all
build-all: clean
	@echo "Building for all platforms..."
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-linux-amd64 main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-linux-arm64 main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-darwin-amd64 main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-darwin-arm64 main.go
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-windows-amd64.exe main.go
	@echo "Build complete. Binaries are in dist/"

.PHONY: build-linux
build-linux: clean
	@echo "Building for Linux..."
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-linux-amd64 main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-linux-arm64 main.go

.PHONY: build-darwin
build-darwin: clean
	@echo "Building for macOS..."
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-darwin-amd64 main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -v --ldflags="-w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o dist/chat-agent-darwin-arm64 main.go

.PHONY: release
release: build-all
	@echo "Creating release archives..."
	cd dist && tar -czf chat-agent-linux-amd64.tar.gz chat-agent-linux-amd64
	cd dist && tar -czf chat-agent-linux-arm64.tar.gz chat-agent-linux-arm64
	cd dist && tar -czf chat-agent-darwin-amd64.tar.gz chat-agent-darwin-amd64
	cd dist && tar -czf chat-agent-darwin-arm64.tar.gz chat-agent-darwin-arm64
	cd dist && zip -q chat-agent-windows-amd64.zip chat-agent-windows-amd64.exe
	@echo "Release archives created in dist/"

.PHONY: clean
clean:
	rm -rf dist

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:
	go vet ./...
	golangci-lint run

.PHONY: install
install: build
	cp dist/chat-agent /usr/local/bin/chat-agent