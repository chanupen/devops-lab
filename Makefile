BINARY  := lab
PKG     := ./cmd/lab
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-all test lint fmt clean doctor

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

build-all:
	GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-amd64 $(PKG)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-darwin-amd64 $(PKG)

test:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

doctor: build
	bin/$(BINARY) doctor

clean:
	rm -rf bin
