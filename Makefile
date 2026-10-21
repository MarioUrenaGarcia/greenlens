# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García

GO    ?= go
GOBIN := $(shell $(GO) env GOPATH)/bin
BIN   := bin/greenlens
NVML  := bin/greenlens-nvml

# Development tools, pinned so every machine and CI run the same versions.
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.9.0
GITLEAKS_VERSION      := v8.30.1
LEFTHOOK_VERSION      := v2.2.2
GO_LICENSES_VERSION   := v2.0.1
HUGO_VERSION          := v0.167.0

# Tools for the web interface, published on npm as native binaries and
# installed without Node.js. The sha512 values are those of the registry.
NPM_TOOLS            ?= $(HOME)/.cache/greenlens/tools
TYPESCRIPT_VERSION   := 7.0.2
TYPESCRIPT_INTEGRITY := sha512-EYdf2cNg7rgCWJnxCdJ+F3V39O8ihb37eHAu1LK8oAFizgTQbPOK7zHHXbPt8rX24COqODXeI3sIf0fCXG7H/A==
BIOME_VERSION        := 2.5.15
BIOME_INTEGRITY      := sha512-xE4iEW/3LqlYj9GFgGrFsFSH51dEEpUbYWBfeOv0q87WkUDxK2o/HhreSb7qMJqck70RVM6Lg96hgedcAYcOkA==
TSC                  := $(NPM_TOOLS)/typescript-$(TYPESCRIPT_VERSION)/lib/tsc
BIOME                := $(NPM_TOOLS)/biome-$(BIOME_VERSION)/biome

SITE_BASE_URL ?= https://mariourenagarcia.github.io/greenlens/

# Licenses accepted for dependencies. GreenLens itself is AGPL-3.0-only and is
# ignored. psx (a dependency of go-landlock) is dual licensed
# "BSD-3-Clause OR GPL-2.0-only" and is used under BSD-3-Clause; go-licenses
# reports both options, so it is ignored here and its full license text is
# reproduced in THIRD_PARTY_NOTICES.md.
ALLOWED_LICENSES := MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC
LICENSE_IGNORES  := --ignore github.com/MarioUrenaGarcia/greenlens \
                    --ignore kernel.org/pub/linux/libs/security/libcap/psx

.DEFAULT_GOAL := build

.PHONY: build static-check test vet lint vuln secrets typography headers i18n check-scripts \
        licenses notices notices-check tidy-check web docs docs-check demo demo-check man \
        site hugo tools hooks check clean

# greenlens is static. greenlens-nvml loads the NVIDIA library through the
# dynamic loader, which makes it a dynamic executable, so only it may import
# purego.
build: static-check
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/greenlens
	CGO_ENABLED=0 $(GO) build -trimpath -o $(NVML) ./cmd/greenlens-nvml

static-check:
	@! $(GO) list -deps ./cmd/greenlens | grep -qx 'github.com/ebitengine/purego' \
		|| { echo "cmd/greenlens imports purego and would not be static"; exit 1; }

# The race detector needs cgo; release binaries are built without it.
test:
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint:
	$(GOBIN)/golangci-lint run ./...

vuln:
	$(GOBIN)/govulncheck ./...

secrets:
	$(GOBIN)/gitleaks dir --no-banner --redact .
	@if git rev-parse --verify -q HEAD >/dev/null; then \
		$(GOBIN)/gitleaks git --no-banner --redact .; \
	fi

typography:
	scripts/check-typography.sh

headers:
	scripts/check-headers.sh

i18n:
	scripts/check-i18n.sh

check-scripts:
	scripts/test-checks.sh

licenses:
	$(GOBIN)/go-licenses check ./... --allowed_licenses=$(ALLOWED_LICENSES) $(LICENSE_IGNORES)

notices:
	$(GO) run ./scripts/gen-notices > THIRD_PARTY_NOTICES.md

notices-check:
	@$(GO) run ./scripts/gen-notices | diff -u THIRD_PARTY_NOTICES.md - \
		|| { echo "THIRD_PARTY_NOTICES.md is stale; run make notices"; exit 1; }

tidy-check:
	$(GO) mod tidy -diff

web:
	$(GO) run web/build.go

# Reference pages generated from the code.
docs:
	$(GO) run ./scripts/gen-docs

docs-check:
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && cp -r docs "$$tmp/docs" && \
		$(GO) run ./scripts/gen-docs -root "$$tmp/docs" -schema "$$tmp/config.schema.json" && \
		diff -ru docs "$$tmp/docs" && diff -u api/config.schema.json "$$tmp/config.schema.json" \
		|| { echo "generated documentation is stale; run make docs"; exit 1; }

# Manual pages in English and Spanish, for packages: dist/man/man1,
# dist/man/man5 and the same under dist/man/es.
MAN_DATE ?= $(shell git log -1 --format=%cs 2>/dev/null)
MAN_VERSION ?= $(shell git describe --tags --always 2>/dev/null)

man:
	$(GO) run ./scripts/gen-man -out dist/man -date "$(MAN_DATE)" -version "$(MAN_VERSION)"

# Animated terminal sessions shown in the README and on the site.
DEMOS := site/static/images/terminal-en.svg site/static/images/terminal-es.svg

demo:
	$(GO) run ./scripts/gen-demo -lang en -o site/static/images/terminal-en.svg
	$(GO) run ./scripts/gen-demo -lang es -o site/static/images/terminal-es.svg

demo-check:
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		$(GO) run ./scripts/gen-demo -lang en -o "$$tmp/terminal-en.svg" && \
		$(GO) run ./scripts/gen-demo -lang es -o "$$tmp/terminal-es.svg" && \
		diff -q "$$tmp/terminal-en.svg" site/static/images/terminal-en.svg >/dev/null && \
		diff -q "$$tmp/terminal-es.svg" site/static/images/terminal-es.svg >/dev/null \
		|| { echo "terminal demos are stale; run make demo"; exit 1; }

# Documentation site for GitHub Pages, written to site/public.
site:
	$(GOBIN)/hugo --source site --destination public --cleanDestinationDir --minify --baseURL "$(SITE_BASE_URL)"

hugo:
	CGO_ENABLED=0 $(GO) install github.com/gohugoio/hugo@$(HUGO_VERSION)

tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)
	$(GO) install github.com/evilmartians/lefthook/v2@$(LEFTHOOK_VERSION)
	$(GO) install github.com/google/go-licenses/v2@$(GO_LICENSES_VERSION)
	scripts/install-npm-tool.sh @typescript/typescript-linux-x64 $(TYPESCRIPT_VERSION) $(TYPESCRIPT_INTEGRITY) $(NPM_TOOLS)/typescript-$(TYPESCRIPT_VERSION)
	scripts/install-npm-tool.sh @biomejs/cli-linux-x64 $(BIOME_VERSION) $(BIOME_INTEGRITY) $(NPM_TOOLS)/biome-$(BIOME_VERSION)

hooks:
	$(GOBIN)/lefthook install

check: tidy-check static-check vet lint test vuln secrets typography headers i18n check-scripts licenses notices-check \
       docs-check demo-check

clean:
	rm -rf bin site/public site/resources
