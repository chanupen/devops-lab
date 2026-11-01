BINARY  := lab
PKG     := ./cmd/lab
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-all reaper test lint fmt clean doctor

# Static like the release binaries: smaller (no cgo) and no dynamic loader at
# exec. go test -race still needs cgo, so only the build targets set it.
build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

# The TTL reaper Lambda for the AWS baseline: linux/arm64, provided.al2023.
# Fixed timestamps and no VCS stamp keep the zip byte-identical between
# builds, so tofu only redeploys the function when its code changes.
reaper:
	mkdir -p bin/reaper
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -buildvcs=false -ldflags '-s -w' \
		-o bin/reaper/bootstrap ./cmd/reaper
	touch -t 202601010000 bin/reaper/bootstrap
	cd bin/reaper && rm -f ../reaper.zip && zip -q -X ../reaper.zip bootstrap

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
