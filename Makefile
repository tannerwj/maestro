VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: test build install release run inspect-config inspect-state inspect-runs reset-issue cleanup-workspaces smoke-gitlab smoke-linear smoke-multi-source smoke-many-sources

test:
	go test ./...

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/maestro ./cmd/maestro

release:
	@test -n "$(VERSION)" || (echo "VERSION is required, for example: make release VERSION=v0.1.0" && exit 1)
	./scripts/build_release.sh "$(VERSION)"

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/maestro

run:
	@test -n "$(CONFIG)" || (echo "CONFIG is required, for example: make run CONFIG=demo/gitlab-claude-auto/maestro.yaml" && exit 1)
	go run -ldflags "$(LDFLAGS)" ./cmd/maestro run --config "$(CONFIG)"

inspect-config:
	@test -n "$(CONFIG)" || (echo "CONFIG is required" && exit 1)
	go run -ldflags "$(LDFLAGS)" ./cmd/maestro inspect config --config "$(CONFIG)"

inspect-state:
	@if [ -n "$(STATE_DIR)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro inspect state --state-dir "$(STATE_DIR)"; \
	elif [ -n "$(CONFIG)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro inspect state --config "$(CONFIG)"; \
	else \
		echo "CONFIG or STATE_DIR is required"; \
		exit 1; \
	fi

inspect-runs:
	@if [ -n "$(STATE_DIR)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro inspect runs --state-dir "$(STATE_DIR)"; \
	elif [ -n "$(CONFIG)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro inspect runs --config "$(CONFIG)"; \
	else \
		echo "CONFIG or STATE_DIR is required"; \
		exit 1; \
	fi

reset-issue:
	@test -n "$(ISSUE)" || (echo "ISSUE is required, for example: make reset-issue CONFIG=demo/gitlab-claude-auto/maestro.yaml ISSUE=group/project#123" && exit 1)
	@if [ -n "$(STATE_DIR)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro reset issue --state-dir "$(STATE_DIR)" $(if $(WORKSPACE_ROOT),--workspace-root "$(WORKSPACE_ROOT)",) "$(ISSUE)"; \
	elif [ -n "$(CONFIG)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro reset issue --config "$(CONFIG)" "$(ISSUE)"; \
	else \
		echo "CONFIG or STATE_DIR is required"; \
		exit 1; \
	fi

cleanup-workspaces:
	@if [ -n "$(CONFIG)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro cleanup workspaces --config "$(CONFIG)" $(if $(DRY_RUN),--dry-run,); \
	elif [ -n "$(WORKSPACE_ROOT)" ] && [ -n "$(STATE_DIR)" ]; then \
		go run -ldflags "$(LDFLAGS)" ./cmd/maestro cleanup workspaces --workspace-root "$(WORKSPACE_ROOT)" --state-dir "$(STATE_DIR)" $(if $(DRY_RUN),--dry-run,); \
	else \
		echo "CONFIG is required, or provide both WORKSPACE_ROOT and STATE_DIR"; \
		exit 1; \
	fi

smoke-gitlab:
	./scripts/smoke_gitlab.sh

smoke-linear:
	./scripts/smoke_linear.sh

smoke-multi-source:
	./scripts/smoke_multi_source.sh

smoke-many-sources:
	./scripts/smoke_many_sources.sh
