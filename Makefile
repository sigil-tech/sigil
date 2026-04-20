SHELL  := bash
GO     := go
BIN    := ./bin

COV_MIN := 50

CMDS    := ./cmd/sigild/ ./cmd/sigilctl/
PLUGINS := $(wildcard ./plugins/sigil-plugin-*/)

.PHONY: all fmt fmt-check vet lint staticcheck test test-race check build build-app install run \
        status generate coverage clean sync-assets hooks fetch-sigil-os-image fetch-vz-binary \
        check-ledger-append-only help

## all: default target — build everything.
all: build

## ---------- Formatting & Linting ------------------------------------------

## fmt: format all Go source files in place (all modules).
fmt:
	@gofmt -w .

## fmt-check: verify formatting without modifying files (CI-safe).
fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt: the following files need formatting:"; gofmt -l .; exit 1; }

## vet: run go vet with shadow analysis.
vet:
	@$(GO) vet ./...

## lint: run staticcheck if installed, skip gracefully otherwise.
lint:
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed — skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; fi

## ---------- Testing -------------------------------------------------------

## test: run all tests.
test:
	@$(GO) test ./...

## test-race: run all tests with the race detector enabled.
test-race:
	@$(GO) test -race ./...

## coverage: run tests with coverage and enforce COV_MIN% gate on internal/.
coverage:
	@$(GO) test ./internal/... -coverprofile=cover.out -covermode=atomic
	@total=$$($(GO) tool cover -func=cover.out | awk '/^total:/ {gsub(/%/,"",$$NF); print $$NF}'); \
	echo "Internal coverage: $${total}% (minimum: $(COV_MIN)%)"; \
	if [ $$(echo "$${total} < $(COV_MIN)" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage $${total}% is below $(COV_MIN)% gate"; \
		rm -f cover.out; \
		exit 1; \
	fi
	@rm -f cover.out

## ---------- Build ---------------------------------------------------------

## build: compile sigild, sigilctl, and all plugins into ./bin/.
build: sync-assets | $(BIN)
	@$(GO) build -o $(BIN)/ $(CMDS)
	@for p in $(PLUGINS); do $(GO) build -o $(BIN)/ $$p; done

$(BIN):
	@mkdir -p $(BIN)

## sync-assets: copy shell hooks and service files into the embed directory.
sync-assets:
	@cp scripts/shell-hook.zsh  internal/assets/scripts/shell-hook.zsh
	@cp scripts/shell-hook.bash internal/assets/scripts/shell-hook.bash
	@cp deploy/sigild.service   internal/assets/deploy/sigild.service

## build-app: compile sigil-app (requires Wails CLI and Node.js).
build-app:
	@cd cmd/sigil-app && wails build -o ../../$(BIN)/sigil-app

## install: build and install all binaries to $GOPATH/bin.
install: sync-assets
	@$(GO) install $(CMDS) $(PLUGINS)
	@echo ""
	@echo "Installed to $$($(GO) env GOPATH)/bin/"
	@echo "Run 'sigild init' to complete setup."

## ---------- CI gate -------------------------------------------------------

## check: CI-safe gate — verify formatting, vet, lint, test with race detector.
check: fmt-check vet lint test-race

## ---------- Dev helpers ---------------------------------------------------

## hooks: install git pre-commit hook (auto-formats Go on commit).
hooks:
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook installed"

## generate: re-generate mocks via mockery.
generate:
	@mockery

## run: build and start sigild with the dev config, watching ~/workspace.
run: build
	@mkdir -p ~/.local/share/sigild
	$(BIN)/sigild -config dev.toml

## status: query the running daemon via sigilctl.
status: build
	$(BIN)/sigilctl status

## fetch-sigil-os-image: download + SHA-verify a pinned sigil-os QCOW2 image for integration tests.
## Set SIGIL_OS_IMAGE_URL to the full URL of the .qcow2 file (a .sha256 sidecar must exist at <URL>.sha256).
## The image is written to testdata/sigil-os.qcow2; the checksum to testdata/sigil-os.qcow2.checksum.
## See docs/build.md for the canonical image URL format.
fetch-sigil-os-image:
	@if [ -z "$$SIGIL_OS_IMAGE_URL" ]; then \
	  echo "SIGIL_OS_IMAGE_URL env var required; see docs/build.md for the canonical URL format."; \
	  echo "Example: SIGIL_OS_IMAGE_URL=https://github.com/sigil-tech/sigil-os/releases/download/v0.3.1/sigil-vm-linux-x86_64.qcow2 make fetch-sigil-os-image"; \
	  exit 1; \
	fi
	@mkdir -p testdata
	curl -L -o testdata/sigil-os.qcow2.checksum "$$SIGIL_OS_IMAGE_URL.sha256"
	curl -L -o testdata/sigil-os.qcow2 "$$SIGIL_OS_IMAGE_URL"
	cd testdata && sha256sum -c sigil-os.qcow2.checksum
	@echo "sigil-os image verified and saved to testdata/sigil-os.qcow2"

## fetch-vz-binary: download + SHA-verify a pinned sigild-vz macOS helper binary next to sigild.
## Set SIGILD_VZ_URL to the full URL of the sigild-vz binary (a .sha256 sidecar must exist at <URL>.sha256).
## The binary is placed at bin/sigild-vz so vmdriver_darwin.go's sibling-of-os.Args[0] discovery resolves
## it for local dev runs. macOS CI runs this target before `go test -tags=darwin ./internal/vmdriver/...`.
## Published by sigil-launcher-macos CI (release.yml) on every release tag; see ADR-028a §7.
fetch-vz-binary:
	@if [ -z "$$SIGILD_VZ_URL" ]; then \
	  echo "SIGILD_VZ_URL env var required; points to a sigild-vz release artefact in sigil-launcher-macos."; \
	  echo "Example: SIGILD_VZ_URL=https://github.com/sigil-tech/sigil-launcher-macos/releases/download/v0.2.0/sigild-vz make fetch-vz-binary"; \
	  exit 1; \
	fi
	@mkdir -p $(BIN)
	curl -L -o $(BIN)/sigild-vz.checksum "$$SIGILD_VZ_URL.sha256"
	curl -L -o $(BIN)/sigild-vz "$$SIGILD_VZ_URL"
	chmod +x $(BIN)/sigild-vz
	cd $(BIN) && sha256sum -c sigild-vz.checksum
	@echo "sigild-vz verified and saved to $(BIN)/sigild-vz"

## check-ledger-append-only: grep for any UPDATE / DELETE / DROP against `ledger` or `ledger_keys`
## tables anywhere outside `internal/ledger/purge.go`. Enforces spec 029 FR-002 / FR-013b — the
## ledger is append-only by design, violations are a build-blocking defect.
## Allowlist: internal/ledger/purge.go (the single authorised purge helper), *.md,
## *_test.go (tests are allowed to poke raw SQL for tamper fixtures).
check-ledger-append-only:
	@set -e; \
	pattern='(UPDATE[[:space:]]+ledger(_keys)?\b|DELETE[[:space:]]+FROM[[:space:]]+ledger(_keys)?\b|DROP[[:space:]]+TABLE[[:space:]]+(IF[[:space:]]+EXISTS[[:space:]]+)?ledger(_keys)?\b)'; \
	matches=$$(git grep -n -i -E "$$pattern" -- \
	  ':(exclude)internal/ledger/purge.go' \
	  ':(exclude)**/*.md' \
	  ':(exclude)Makefile' \
	  ':(exclude)**/*_test.go' \
	  2>/dev/null || true); \
	if [ -n "$$matches" ]; then \
	  echo "check-ledger-append-only: FAIL — disallowed ledger mutation outside internal/ledger/purge.go:"; \
	  echo "$$matches"; \
	  exit 1; \
	fi; \
	echo "check-ledger-append-only: OK (ledger / ledger_keys tables only touched via Append + purge helper)"

## clean: remove build artifacts.
clean:
	@rm -rf $(BIN) cover.out coverage.out
	@# Legacy root-level binaries.
	@rm -f sigild sigilctl sigil-plugin-*

## ---------- Help ----------------------------------------------------------

## help: show this help.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'
