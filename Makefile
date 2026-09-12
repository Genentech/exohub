SHELL := /bin/bash

GITBRANCH = $(shell git rev-parse --abbrev-ref HEAD)
GITHASH = $(shell git rev-parse --short HEAD)
GITTAG = $(shell git describe --tags --exact-match HEAD 2> /dev/null)
GITTAG_CLEAN = $(subst v,,$(GITTAG))
BUILD_TAG = $(if $(GITTAG),$(GITTAG_CLEAN),$(GITBRANCH)-$(GITHASH))

DOCKER_REPOSITORY = 347633553755.dkr.ecr.us-west-2.amazonaws.com
DOCKER_LOCAL_REPOSITORY = localhost:5000
ATLAS_IMAGE = gp/exohub-atlas

.PHONY: exocli exocli-version exo-serve-assets image push dev-push git-annex-remote-s5cmd git-annex-remote-artifactdb-export git-annex-remote-drive exo-credential-helper adb-capture adb-standalone binaries build test test-integration test-integration-coverage test-all test-all-coverage test-coverage-html test-all-coverage-html leak-scan oss-export oss-export-skip-verify oss-sync oss-sync-dry-run
exocli:
	@mkdir -p bin .gocache
	(cd go/exo && GOCACHE=$(PWD)/.gocache go build -tags artifactdb -o ../../bin/exo .)

exocli-version:
	@mkdir -p bin .gocache
	@VERSION=$$(git describe --tags --abbrev=0 2>/dev/null || git describe --tags --always --dirty); \
	COMMIT=$$(git rev-parse --short HEAD); \
	DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ); \
	GOCACHE=$(PWD)/.gocache go build -tags artifactdb -o bin/exo \
		-ldflags "-X main.version=$$VERSION -X main.commit=$$COMMIT -X main.date=$$DATE -X github.com/Genentech/exohub/go/exo/commands/auth.builtinDriveClientID=$${GOOGLE_DRIVE_CLIENT_ID:-PLACEHOLDER_CLIENT_ID} -X github.com/Genentech/exohub/go/exo/commands/auth.builtinDriveClientSecret=$${GOOGLE_DRIVE_CLIENT_SECRET:-PLACEHOLDER_CLIENT_SECRET}" \
		./go/exo

exo-serve-assets:
	@mkdir -p go/exo/commands/serve/static/xterm
	@if [ ! -f go/exo/commands/serve/static/xterm/xterm.js ]; then \
		echo "Downloading xterm.js assets..."; \
		cd /tmp && rm -rf exo-xterm-install && mkdir exo-xterm-install && cd exo-xterm-install && \
		npm install xterm@5.3.0 xterm-addon-fit@0.8.0 2>/dev/null && \
		cp node_modules/xterm/lib/xterm.js $(CURDIR)/go/exo/commands/serve/static/xterm/ && \
		cp node_modules/xterm/css/xterm.css $(CURDIR)/go/exo/commands/serve/static/xterm/ && \
		cp node_modules/xterm-addon-fit/lib/xterm-addon-fit.js $(CURDIR)/go/exo/commands/serve/static/xterm/ && \
		rm -rf /tmp/exo-xterm-install; \
	else \
		echo "xterm.js assets already present"; \
	fi

image: binaries
	docker build -t ${ATLAS_IMAGE}:${BUILD_TAG} -f Dockerfile .

push:
	docker tag ${ATLAS_IMAGE}:${BUILD_TAG} ${DOCKER_REPOSITORY}/${ATLAS_IMAGE}:${BUILD_TAG}
	docker push ${DOCKER_REPOSITORY}/${ATLAS_IMAGE}:${BUILD_TAG}

dev-push:
	docker tag ${ATLAS_IMAGE}:${BUILD_TAG} ${DOCKER_LOCAL_REPOSITORY}/${ATLAS_IMAGE}:${BUILD_TAG}
	docker tag ${ATLAS_IMAGE}:${BUILD_TAG} ${DOCKER_LOCAL_REPOSITORY}/${ATLAS_IMAGE}:latest
	docker push ${DOCKER_LOCAL_REPOSITORY}/${ATLAS_IMAGE}:${BUILD_TAG}
	docker push ${DOCKER_LOCAL_REPOSITORY}/${ATLAS_IMAGE}:latest

adb-capture:
	@mkdir -p bin .gocache
	(cd go/adb-standalone/tools/adb-capture && GOCACHE=$(PWD)/.gocache go build -o ../../../../bin/adb-capture .)

adb-standalone:
	@mkdir -p bin .gocache
	(cd go/adb-standalone && GOCACHE=$(PWD)/.gocache go build -o ../../bin/adb-standalone .)

git-annex-remote-s5cmd:
	@mkdir -p bin .gocache
	(cd go/git-annex-remote-s5cmd && GOCACHE=$(PWD)/.gocache go build -o ../../bin/git-annex-remote-s5cmd .)

git-annex-remote-artifactdb-export:
	@mkdir -p bin .gocache
	(cd go/git-annex-remote-artifactdb-export && GOCACHE=$(PWD)/.gocache go build -tags artifactdb -o ../../bin/git-annex-remote-artifactdb-export .)

git-annex-remote-drive:
	@mkdir -p bin .gocache
	(cd go/git-annex-remote-drive && GOCACHE=$(PWD)/.gocache go build -ldflags "-X main.builtinClientID=$${GOOGLE_DRIVE_CLIENT_ID:-PLACEHOLDER_CLIENT_ID} -X main.builtinClientSecret=$${GOOGLE_DRIVE_CLIENT_SECRET:-PLACEHOLDER_CLIENT_SECRET}" -o ../../bin/git-annex-remote-drive .)
exo-credential-helper:
	@mkdir -p bin .gocache
	(cd go/exo-credential-helper && GOCACHE=$(PWD)/.gocache go build  -o ../../bin/exo-credential-helper .)

.PHONY: binaries
binaries:
	rm -rf dist dist-exo dist-adb-standalone dist-git-annex-remote-s5cmd dist-git-annex-remote-artifactdb-export dist-git-annex-remote-drive dist-exo-credential-helper
	GOCACHE=$(PWD)/.gocache GORELEASER_CURRENT_TAG=$${GORELEASER_CURRENT_TAG:-v0.0.0} goreleaser build --snapshot --clean --config .goreleaser-exo.yml
	GOCACHE=$(PWD)/.gocache GORELEASER_CURRENT_TAG=$${GORELEASER_CURRENT_TAG:-v0.0.0} goreleaser build --snapshot --clean --config .goreleaser-adb-standalone.yml
	GOCACHE=$(PWD)/.gocache GORELEASER_CURRENT_TAG=$${GORELEASER_CURRENT_TAG:-v0.0.0} goreleaser build --snapshot --clean --config .goreleaser-git-annex-remote-s5cmd.yml
	GOCACHE=$(PWD)/.gocache GORELEASER_CURRENT_TAG=$${GORELEASER_CURRENT_TAG:-v0.0.0} goreleaser build --snapshot --clean --config .goreleaser-git-annex-remote-artifactdb-export.yml
	GOCACHE=$(PWD)/.gocache GORELEASER_CURRENT_TAG=$${GORELEASER_CURRENT_TAG:-v0.0.0} goreleaser build --snapshot --clean --config .goreleaser-exo-credential-helper.yml
	GOCACHE=$(PWD)/.gocache GORELEASER_CURRENT_TAG=$${GORELEASER_CURRENT_TAG:-v0.0.0} goreleaser build --snapshot --clean --config .goreleaser-git-annex-remote-drive.yml
	mkdir -p dist
	cp -a dist-exo/* dist/
	cp -a dist-adb-standalone/* dist/
	cp -a dist-git-annex-remote-s5cmd/* dist/
	cp -a dist-git-annex-remote-artifactdb-export/* dist/
	cp -a dist-exo-credential-helper/* dist/
	cp -a dist-git-annex-remote-drive/* dist/

build: binaries

test:
	@mkdir -p .gocache .coverage
	@printf "go 1.24.0\n\nuse (\n\t../go/exo\n\t../go/git-annex-remote-s5cmd\n\t../go/git-annex-remote-artifactdb-export\n\t../go/exo-credential-helper\n)\n" > .coverage/go.work
	(cd go/exo && GOCACHE=$(CURDIR)/.gocache go test -coverprofile=../../.coverage/exo.out ./...)
	(cd go/exo && GOCACHE=$(CURDIR)/.gocache go test -tags artifactdb -coverprofile=../../.coverage/exo-artifactdb.out ./...)
	(cd go/git-annex-remote-s5cmd && GOCACHE=$(CURDIR)/.gocache go test -coverprofile=../../.coverage/s5cmd.out ./...)
	(cd go/git-annex-remote-artifactdb-export && GOCACHE=$(CURDIR)/.gocache go test -tags artifactdb -coverprofile=../../.coverage/artifactdb-export.out ./...)
	(cd go/exo-credential-helper && GOCACHE=$(CURDIR)/.gocache go test -coverprofile=../../.coverage/credential-helper.out ./...)
	@printf "mode: set\n" > coverage.out
	@tail -n +2 .coverage/exo.out >> coverage.out
	@tail -n +2 .coverage/exo-artifactdb.out >> coverage.out
	@tail -n +2 .coverage/s5cmd.out >> coverage.out
	@tail -n +2 .coverage/artifactdb-export.out >> coverage.out
	@tail -n +2 .coverage/credential-helper.out >> coverage.out

# Run integration tests with coverage collection
test-integration-coverage:
	@mkdir -p .coverage/integration
	@echo "Running integration tests with coverage instrumentation..."
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/manifest_validation.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/fsck_subdir.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/heartbeat_storage.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/version.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/export_paths.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/link_files.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/info_display.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/context_management.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/broadcast_message.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/pull_content.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/copy_remotes.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/sync_import_dryrun.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/init_remote.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/uuid_cleanup.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/uuid_type_matching.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/clone_preset.sh
	@EXO_COVERAGE_DIR=$(CURDIR)/.coverage/integration scripts/integration/remote_rename.sh
	@echo "Converting integration test coverage data..."
	@go tool covdata textfmt -i=.coverage/integration -o=.coverage/integration.out

# Merge unit test and integration test coverage
test-all-coverage: test test-integration-coverage
	@echo "Merging unit and integration test coverage..."
	@printf "mode: set\n" > coverage-all.out
	@tail -n +2 .coverage/exo.out >> coverage-all.out
	@tail -n +2 .coverage/s5cmd.out >> coverage-all.out
	@tail -n +2 .coverage/artifactdb-export.out >> coverage-all.out
	@tail -n +2 .coverage/credential-helper.out >> coverage-all.out
	@tail -n +2 .coverage/integration.out >> coverage-all.out
	@echo "Global coverage report generated: coverage-all.out"
	@echo ""
	@echo "=== Global Coverage Summary ==="
	@awk 'NR>1{covered+=$$NF=="1"; total++} END{printf "Total: %.1f%% (%d/%d statements)\n", covered*100/total, covered, total}' coverage-all.out

test-integration:
	@echo "Running integration tests..."
	@scripts/integration/manifest_validation.sh
	@scripts/integration/fsck_subdir.sh
	@scripts/integration/heartbeat_storage.sh
	@scripts/integration/version.sh
	@scripts/integration/export_paths.sh
	@scripts/integration/link_files.sh
	@scripts/integration/info_display.sh
	@scripts/integration/context_management.sh
	@scripts/integration/broadcast_message.sh
	@scripts/integration/pull_content.sh
	@scripts/integration/copy_remotes.sh
	@scripts/integration/sync_import_dryrun.sh
	@scripts/integration/init_remote.sh
	@scripts/integration/uuid_cleanup.sh
	@scripts/integration/uuid_type_matching.sh
	@scripts/integration/clone_preset.sh
	@scripts/integration/remote_rename.sh

test-all: test test-integration

test-coverage-html:
	@GOWORK=$(CURDIR)/.coverage/go.work go tool cover -html=coverage.out -o coverage.html
	@echo "Unit test HTML coverage report: coverage.html"

test-all-coverage-html: test-all-coverage
	@GOWORK=$(CURDIR)/.coverage/go.work go tool cover -html=coverage-all.out -o coverage-all.html
	@echo "Global HTML coverage report: coverage-all.html"
	@echo "Open with: open coverage-all.html  (macOS) or xdg-open coverage-all.html  (Linux)"

leak-scan:
	@bash scripts/leak-scan.sh --skip-gitleaks

# Bootstrap-only — not for ongoing use.
# oss-export was used for the one-time initial public drop (oss-build-tags-and-export).
# For regular publishing use oss-sync / oss-sync-dry-run below (internal→public,
# owner-driven via scripts/oss-sync.sh).  oss-export is kept here for
# disaster-recovery / re-bootstrap only.  See docs/oss-sync.md.
oss-export:
	@bash scripts/oss-export.sh

oss-export-skip-verify:
	@bash scripts/oss-export.sh --skip-verify --skip-gitleaks

# oss-sync: sync the staged OSS tree into a local clone of github.com/Genentech/exohub.
# The owner runs `git -C <public-dir> push` after this step.
# Requires: --public-dir <path to local clone of Genentech/exohub>
# Example:  make oss-sync-dry-run PUBLIC_DIR=~/repos/exohub
#           make oss-sync         PUBLIC_DIR=~/repos/exohub
oss-sync:
	@bash scripts/oss-sync.sh --public-dir "$(PUBLIC_DIR)"

oss-sync-dry-run:
	@bash scripts/oss-sync.sh --public-dir "$(PUBLIC_DIR)" --dry-run
