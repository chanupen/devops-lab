BINARY  := lab
PKG     := ./cmd/lab
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
# Where make install puts lab: make install BINDIR=/some/dir. No sudo.
BINDIR  ?= $(HOME)/.local/bin
bindir  := $(patsubst %/,%,$(BINDIR))

.PHONY: build build-all install uninstall reaper test lint fmt clean doctor

# Static like the release binaries: smaller (no cgo) and no dynamic loader at
# exec. go test -race still needs cgo, so only the build targets set it.
build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

# install puts the binary just built on PATH, so an older lab does not keep
# running against a newer lab.yaml. It warns, without failing, when BINDIR
# is not on PATH or when another lab on PATH comes first.
install: build
	install -D -m 0755 bin/$(BINARY) $(bindir)/$(BINARY)
	@echo "installed $(bindir)/$(BINARY), version $$($(bindir)/$(BINARY) version)"
	@case ":$$PATH:" in \
		*":$(bindir):"*|*":$(bindir)/:"*) ;; \
		*) echo "warning: $(bindir) is not on PATH; add it to PATH, or run $(bindir)/$(BINARY)" ;; \
	esac
	@found=$$(command -v $(BINARY) 2>/dev/null || true); \
	if [ -n "$$found" ] && [ "$$(readlink -f "$$found")" != "$$(readlink -f "$(bindir)/$(BINARY)")" ]; then \
		echo "warning: $(BINARY) on PATH is $$found, not $(bindir)/$(BINARY), and runs instead of it; if it is an older copy, remove it: rm $$found"; \
	fi

# uninstall removes $(BINDIR)/lab only.
uninstall:
	rm -f $(bindir)/$(BINARY)

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
