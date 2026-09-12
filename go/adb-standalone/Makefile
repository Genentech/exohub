.PHONY: build test seed serve clean

BINARY := adb-standalone
NDJSON ?= ../../docs/adb-standalone/fixtures/seed/documents.ndjson

build:
	go build -o $(BINARY) .

test:
	go test ./...

# Run with a live SurrealDB binary for integration tests.
# Usage: make test-integration SURREAL_BIN=/usr/local/bin/surreal
test-integration:
	SURREAL_BIN=$(SURREAL_BIN) go test -v ./...

seed: build
	./$(BINARY) seed $(NDJSON)

serve: build
	./$(BINARY) serve

clean:
	rm -f $(BINARY)
