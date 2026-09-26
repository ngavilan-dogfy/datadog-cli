VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -ldflags "-X github.com/ngavilan-dogfy/datadog-cli/cmd.Version=$(VERSION)"
BIN      = datadog-cli
PREFIX  ?= /usr/local

.PHONY: build test vet check install clean

build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/datadog

test:
	go test ./...

vet:
	go vet ./...

check: vet test build

install: build
	install -m 0755 $(BIN) $(PREFIX)/bin/datadog

clean:
	rm -f $(BIN)
