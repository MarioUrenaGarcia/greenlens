# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García

GO    ?= go
GOBIN := $(shell $(GO) env GOPATH)/bin
BIN   := bin/greenlens

# Development tools, pinned so every machine and CI run the same versions.
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.9.0
GITLEAKS_VERSION      := v8.30.1
LEFTHOOK_VERSION      := v2.2.2
GO_LICENSES_VERSION   := v2.0.1

# Licenses accepted for dependencies. GreenLens itself is AGPL-3.0-only and is
# ignored. psx (a dependency of go-landlock) is dual licensed
# "BSD-3-Clause OR GPL-2.0-only" and is used under BSD-3-Clause; go-licenses
# reports both options, so it is ignored here and its full license text is
# reproduced in THIRD_PARTY_NOTICES.md.
ALLOWED_LICENSES := MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC
LICENSE_IGNORES  := --ignore github.com/MarioUrenaGarcia/greenlens \
                    --ignore kernel.org/pub/linux/libs/security/libcap/psx

.DEFAULT_GOAL := build

.PHONY: build test vet lint vuln secrets typography headers i18n check-scripts \
        licenses notices notices-check tidy-check web tools hooks check clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/greenlens

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

tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)
	$(GO) install github.com/evilmartians/lefthook/v2@$(LEFTHOOK_VERSION)
	$(GO) install github.com/google/go-licenses/v2@$(GO_LICENSES_VERSION)

hooks:
	$(GOBIN)/lefthook install

check: tidy-check vet lint test vuln secrets typography headers i18n check-scripts licenses notices-check

clean:
	rm -rf bin
