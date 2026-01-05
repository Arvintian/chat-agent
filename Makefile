GIT_VERSION = $(shell git rev-parse --short HEAD)

.PHONY: build
build:
	go build -v --ldflags="-w -X main.Version=$(GIT_VERSION)" -o dist/chat-agent main.go

clean:
	rm -rf dist