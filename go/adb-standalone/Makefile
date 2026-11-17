.PHONY: build test seed serve clean genfixtures verify-fixtures

BINARY := adb-standalone
NDJSON ?= ../../docs/adb-standalone/fixtures/seed/documents.ndjson
FIXTURES_DIR ?= ../../docs/adb-standalone/fixtures

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

# Regenerate the parity test fixtures (seed/documents.ndjson, search_p1.json,
# search_p2.json) from the deterministic synthetic corpus generator.
# Usage: make genfixtures SURREAL_BIN=/usr/local/bin/surreal
genfixtures:
	SURREAL_BIN=$(SURREAL_BIN) go run ./tools/genfixtures --out $(FIXTURES_DIR)

# Verify the committed fixtures are reproducible: regenerate into a temp dir
# and diff against the committed copies. Fails if the generator and the
# committed fixtures have drifted apart.
# Usage: make verify-fixtures SURREAL_BIN=/usr/local/bin/surreal
verify-fixtures:
	@tmpdir=$$(mktemp -d); \
	mkdir -p $$tmpdir/seed; \
	SURREAL_BIN=$(SURREAL_BIN) go run ./tools/genfixtures --out $$tmpdir; \
	status=0; \
	for f in seed/documents.ndjson search_p1.json search_p2.json; do \
		if ! diff -u "$(FIXTURES_DIR)/$$f" "$$tmpdir/$$f"; then status=1; fi; \
	done; \
	rm -rf $$tmpdir; \
	exit $$status
