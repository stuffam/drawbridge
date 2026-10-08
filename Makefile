# Drawbridge build. Run `make help` to list the targets.

GO  ?= go
NPM ?= npm

# The build-time version constant is a bare string (CLAUDE.md, "Conventions"): the exact
# vX.Y.Z tag on HEAD without its "v", or 0.0.0-dev between releases.
GIT_TAG := $(shell git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null)
VERSION ?= $(if $(GIT_TAG),$(patsubst v%,%,$(GIT_TAG)),0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(if $(shell git status --porcelain 2>/dev/null),-dirty)
# The Debian version nfpm makes of VERSION: the first - becomes ~, so 1.0.0-rc.1 sorts before 1.0.0.
DEB_VERSION := $(subst -,~,$(VERSION))
# The time of the commit being built. nfpm puts it on every file in a package, where it would put
# the time of the checkout, so the same commit gives the same package, byte for byte (docs/PLAN.md
# §11.1). Go's own output doesn't depend on it.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null)
ifneq ($(SOURCE_DATE_EPOCH),)
export SOURCE_DATE_EPOCH
endif

PKG     := github.com/stuffam/drawbridge
# Drawbridge's own Go packages. Not ./..., which would include Go files that npm
# packages ship inside web/node_modules.
GO_PKGS := ./cmd/... ./internal/...
# The integration tests have a build tag, so they're linted with it.
GO_LINT := --build-tags integration $(GO_PKGS) ./test/...
# Run the integration tests as root: directly if we are root, otherwise through sudo.
SUDO_EXEC := $(if $(filter 0,$(shell id -u)),,-exec 'sudo -E')
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)
GOBUILD := CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)'

# Pinned build tools, installed into ./bin by `make tools`.
BIN                   := $(CURDIR)/bin
GOLANGCI_LINT_VERSION := v2.14.0
NFPM_VERSION          := v2.47.0
MISSPELL_VERSION      := v0.8.0
CYCLONEDX_GOMOD_VERSION := v1.12.0
GOVULNCHECK_VERSION   := v1.8.0

ARCHES := arm64 amd64

# npm writes this file when an install finishes, so it, unlike the node_modules directory,
# is missing after an interrupted or emptied install and `make` runs `npm ci` again.
WEB_DEPS := web/node_modules/.package-lock.json

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: tools
tools: $(BIN)/golangci-lint $(BIN)/nfpm $(BIN)/misspell $(BIN)/cyclonedx-gomod $(BIN)/govulncheck ## Install the pinned build tools into ./bin.

# Each tool installs on first use. After changing a version above, run `make clean-tools`.
$(BIN)/golangci-lint:
	GOBIN=$(BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
$(BIN)/nfpm:
	GOBIN=$(BIN) $(GO) install github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION)
$(BIN)/misspell:
	GOBIN=$(BIN) $(GO) install github.com/golangci/misspell/cmd/misspell@$(MISSPELL_VERSION)
$(BIN)/cyclonedx-gomod:
	GOBIN=$(BIN) $(GO) install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)
$(BIN)/govulncheck:
	GOBIN=$(BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

.PHONY: clean-tools
clean-tools: ## Delete ./bin, so the next build installs the pinned tool versions again.
	rm -rf $(BIN)

$(WEB_DEPS): web/package.json web/package-lock.json
	cd web && $(NPM) ci
	@touch $@

.PHONY: web
web: web-build embed ## Build the web app and embed it (web-build, then embed).

.PHONY: web-build
web-build: $(WEB_DEPS) ## Build the web app into web/build/.
	cd web && $(NPM) run build

.PHONY: embed
embed: ## Copy web/build/ into the Go embed directory, internal/webui/dist/.
	@test -f web/build/200.html || { echo "web/build/ is missing; run make web-build first" >&2; exit 1; }
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
	cp -R web/build/. internal/webui/dist/

.PHONY: build
build: ## Build drawbridge for this machine into dist/.
	$(GOBUILD) -o dist/drawbridge ./cmd/drawbridge

.PHONY: build-linux
build-linux: ## Build drawbridge for linux/arm64 and linux/amd64 into dist/linux-<arch>/.
	@for arch in $(ARCHES); do \
		echo "GOARCH=$$arch"; \
		GOOS=linux GOARCH=$$arch $(GOBUILD) -o dist/linux-$$arch/drawbridge ./cmd/drawbridge || exit 1; \
	done

.PHONY: deb
deb: web ## Build the web app, then the .deb packages for arm64 and amd64 into dist/.
	$(MAKE) package

.PHONY: package
package: $(BIN)/nfpm ## Build binaries and .deb packages from the already-embedded web app.
	@test -f internal/webui/dist/200.html || { echo "the web app isn't embedded; run make web first" >&2; exit 1; }
	$(MAKE) build-linux
	@mkdir -p dist/package
	@for arch in $(ARCHES); do \
		cp dist/linux-$$arch/drawbridge dist/package/drawbridge && \
		VERSION=$(VERSION) ARCH=$$arch $(BIN)/nfpm package --config packaging/nfpm.yaml \
			--packager deb --target dist/ || exit 1; \
	done
	@rm -rf dist/package

# What a release holds (docs/PLAN.md §11.1), in dist/release/: both packages, an SBOM for each
# package's binary (its Go modules) and one for the web app (its npm packages), install.sh, and
# SHA256SUMS for all of them. Every file is named for the version, 1.0.0-rc.1, and not the Debian
# version in a package's own Version field, 1.0.0~rc.1: GitHub rewrites a ~ in a file's name to a .,
# which made SHA256SUMS list names the release didn't have (v0.1.0-rc.1).
RELEASE := dist/release

.PHONY: sbom
sbom: deb $(BIN)/cyclonedx-gomod $(WEB_DEPS) ## Write an SBOM for each package's binary and one for the web app, beside the packages in dist/.
	@for arch in $(ARCHES); do \
		$(BIN)/cyclonedx-gomod bin -json -noserial -notimestamp -version v$(VERSION) \
			-output dist/drawbridge_$(VERSION)_$$arch.sbom.json dist/linux-$$arch/drawbridge || exit 1; \
	done
	cd web && $(NPM) sbom --sbom-format cyclonedx > ../dist/drawbridge-web_$(VERSION).sbom.json

.PHONY: release-files
release-files: sbom ## Gather what a release holds into dist/release/, with its SHA256SUMS, and check it.
	rm -rf $(RELEASE)
	mkdir -p $(RELEASE)
	for arch in $(ARCHES); do \
		cp dist/drawbridge_$(DEB_VERSION)_$$arch.deb $(RELEASE)/drawbridge_$(VERSION)_$$arch.deb || exit 1; \
	done
	cp dist/drawbridge*_$(VERSION)*.sbom.json scripts/install.sh $(RELEASE)/
	cd $(RELEASE) && for f in *; do echo "$$f"; done | LC_ALL=C sort | xargs sha256sum > ../SHA256SUMS.tmp
	mv dist/SHA256SUMS.tmp $(RELEASE)/SHA256SUMS
	scripts/release-verify.sh $(RELEASE) $(VERSION)

# Known vulnerabilities in the Go code and what it imports (docs/PLAN.md §10), from the Go vulnerability
# database, so it needs the network. It reports only what the code can reach, and fails on that.
.PHONY: vuln
vuln: $(BIN)/govulncheck ## Check the Go packages and their dependencies for known vulnerabilities (needs the network).
	$(BIN)/govulncheck $(GO_PKGS)

.PHONY: test
test: test-go test-web ## Run all tests.

.PHONY: test-go
test-go: ## Run the Go tests with the race detector, and the write-budget test without it.
	$(GO) test -race $(GO_PKGS)
	# A simulated day of polling takes a minute under the race detector, so TestWriteBudget
	# skips there and runs here.
	$(GO) test -count=1 -run 'TestWriteBudget' ./internal/service

.PHONY: test-integration
test-integration: build ## Run the kernel WireGuard tests in network namespaces (root, IPv6, wireguard module).
	test/integration/preflight.sh
	DRAWBRIDGE_INTEGRATION=1 DRAWBRIDGE_BIN=$(CURDIR)/dist/drawbridge \
		$(GO) test -tags integration -count=1 -v $(SUDO_EXEC) ./test/integration/

.PHONY: test-upgrade
test-upgrade: build ## Upgrade a host from each older build in test/integration/upgrade-from.txt, tunnel up (same needs as test-integration).
	test/integration/preflight.sh
	DRAWBRIDGE_BIN=$(CURDIR)/dist/drawbridge test/integration/upgrade.sh

.PHONY: test-packaging
test-packaging: ## Run the .deb's maintainer scripts through real dpkg and a fake systemctl (root; a throwaway Debian container or VM only).
	DRAWBRIDGE_PACKAGING_TEST=1 $(if $(filter 0,$(shell id -u)),,sudo -E )test/packaging/test.sh

# The release scripts' tests (docs/PLAN.md §12). They install nothing and need no root: git, and
# dpkg-deb for part of the first, and dpkg for the second (a Debian-family host, or a container).
.PHONY: test-release
test-release: ## Test the scripts that decide and check a release (needs git; dpkg-deb for part of it).
	test/release/test.sh

.PHONY: test-install
test-install: ## Test scripts/install.sh against a fake release page (needs a Debian-family dpkg; installs nothing).
	test/install/test.sh

.PHONY: test-web
test-web: $(WEB_DEPS) ## Run the web app's unit tests.
	cd web && $(NPM) test

.PHONY: test-e2e
test-e2e: web build ## Drive the real web app in a browser, against `serve --backend fake` (Playwright).
	cd web && DRAWBRIDGE_BIN=$(CURDIR)/dist/drawbridge npx playwright test

# The docs site (zensical.toml): Zensical, in a virtualenv of its own. It needs Python 3.10 or later,
# so on a machine whose python3 is older, `make docs PYTHON=python3.13`.
PYTHON    ?= python3
DOCS_VENV ?= $(CURDIR)/.venv-docs

$(DOCS_VENV)/bin/zensical: requirements-docs.txt
	$(PYTHON) -m venv $(DOCS_VENV)
	$(DOCS_VENV)/bin/pip install -q -r requirements-docs.txt
	@touch $@

.PHONY: docs
docs: $(DOCS_VENV)/bin/zensical ## Build the docs site into site/ (strictly), and check its lists against GitHub's.
	$(DOCS_VENV)/bin/python -m unittest discover -s test/docs
	$(DOCS_VENV)/bin/zensical build --clean --strict
	$(DOCS_VENV)/bin/python test/docs/check_lists.py docs site

.PHONY: docs-serve
docs-serve: $(DOCS_VENV)/bin/zensical ## Preview the docs site at http://localhost:8000, rebuilt as docs/ changes.
	$(DOCS_VENV)/bin/zensical serve

.PHONY: lint
lint: lint-go lint-web lint-shell spell ## Run every linter and the spelling check.

.PHONY: lint-go
lint-go: $(BIN)/golangci-lint ## Lint and format-check the Go code.
	$(BIN)/golangci-lint run $(GO_LINT)
	$(BIN)/golangci-lint fmt --diff $(GO_PKGS) ./test/...

# The release scripts and their tests. CI has shellcheck; a machine without it skips this and says so.
SHELL_FILES := scripts/install.sh scripts/changelog-release.sh scripts/release-notes.sh scripts/release-check.sh \
	scripts/release-verify.sh \
	test/install/test.sh test/release/test.sh

.PHONY: lint-shell
lint-shell: ## Run shellcheck on the release scripts and their tests (skipped if it isn't installed).
	@if command -v shellcheck >/dev/null 2>&1; then shellcheck $(SHELL_FILES); \
	else echo "lint-shell: shellcheck isn't installed, so the shell scripts weren't checked"; fi

.PHONY: lint-web
lint-web: $(WEB_DEPS) ## Lint, format-check, and type-check the web app.
	cd web && $(NPM) run lint && $(NPM) run check

# Lockfiles and SVGs hold package names and path data, not prose.
SPELL_FILES = git ls-files -z --cached --others --exclude-standard | \
	grep -zv -e 'package-lock\.json$$' -e 'go\.sum$$' -e '\.svg$$'

.PHONY: spell
spell: $(BIN)/misspell ## Check every tracked text file for British spellings (U.S. English only).
	$(SPELL_FILES) | xargs -0 $(BIN)/misspell -locale US -error

.PHONY: fmt
fmt: $(BIN)/golangci-lint $(WEB_DEPS) ## Format the Go code and the web app.
	$(BIN)/golangci-lint fmt $(GO_PKGS) ./test/...
	cd web && $(NPM) run format

.PHONY: check
check: lint test vuln ## Run everything CI runs, except the package build.

.PHONY: clean
clean: ## Delete build output (not ./bin or node_modules).
	rm -rf dist web/build
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
