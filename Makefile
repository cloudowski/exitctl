VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
PKG     := github.com/cloudowski/exitctl
# Shown at the bottom of HTML and Markdown reports (FR-074).
SUPPORT_EMAIL ?= tomasz@cloudowski.com
LDFLAGS := -s -w -X $(PKG)/internal/version.Tool=$(VERSION) -X $(PKG)/internal/version.SupportEmail=$(SUPPORT_EMAIL)
GO      ?= go

.PHONY: build test lint golden-update determinism e2e-offline cross review clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/exitctl ./cmd/exitctl

test:
	$(GO) test ./...

lint:
	golangci-lint run

golden-update:
	$(GO) test ./... -update

determinism:
	$(GO) test ./internal/cli/ -run 'Determinism|MergePermutations|Golden' -count=1

e2e-offline:
	@if command -v unshare >/dev/null 2>&1; then \
		unshare -rn $(GO) test ./internal/cli/... -run TestScript -count=1; \
	else \
		echo "unshare not available: run 'docker run --rm --network none -v $$PWD:/src -w /src golang:1.27 make test'"; \
	fi

cross:
	@for os in linux darwin; do for arch in amd64 arm64; do \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/exitctl-$$os-$$arch ./cmd/exitctl || exit 1; \
	done; done

review:
	$(GO) run ./tools/catalogreview -open

clean:
	rm -rf bin dist
