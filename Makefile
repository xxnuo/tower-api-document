.PHONY: install build-web build run dev test version bump docker-build docker-push

AIR := $(shell command -v air 2>/dev/null || echo $(HOME)/go/bin/air)
VERSION_COMMAND := node -p "require('./oauth-server/web/package.json').version"
IMAGE := registry.lazycat.cloud/x/agentstower
BUMP ?= patch
export BUMP

install:
	cd oauth-server/web && pnpm install

build-web:
	cd oauth-server/web && pnpm build

build: build-web
	cd oauth-server && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/agents-tower .
	@if command -v upx >/dev/null 2>&1; then upx --best --lzma -q oauth-server/bin/agents-tower; fi

run: build-web
	cd oauth-server && go run .

dev: build-web
	@bash -c 'trap "kill 0" EXIT INT TERM; (cd oauth-server && VITE_DEV_URL=http://localhost:5173 "$(AIR)") & (cd oauth-server/web && pnpm dev) & wait'

test: build-web
	cd oauth-server && go test -v ./... && go vet ./...

version:
	@$(VERSION_COMMAND)

bump:
	@case "$$BUMP" in \
		patch|minor|major) cd oauth-server/web && pnpm version "$$BUMP" --no-git-tag-version --no-git-checks ;; \
		*) echo "Usage: make bump BUMP=patch|minor|major" >&2; exit 1 ;; \
	esac

docker-build docker-push:
	@set -e; version=$$($(VERSION_COMMAND)); image="$(IMAGE):$$version"; \
	docker build --load --build-arg VERSION="$$version" --tag "$$image" oauth-server; \
	if [ "$@" = docker-push ]; then \
		docker push "$$image"; \
		docker tag "$$image" "$(IMAGE):latest"; \
		docker push "$(IMAGE):latest"; \
	fi
