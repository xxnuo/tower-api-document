.PHONY: install build-web build run dev test version bump docker-build docker-push

AIR := $(shell command -v air 2>/dev/null || echo $(HOME)/go/bin/air)
VERSION_COMMAND := node -p "require('./server/web/package.json').version"
IMAGE := registry.lazycat.cloud/x/agentstower
BUMP ?= patch
export BUMP

install:
	cd server/web && pnpm install

build-web:
	cd server/web && pnpm build

build: build-web
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/agentstower .
	@if command -v upx >/dev/null 2>&1; then upx --best --lzma -q server/bin/agentstower; fi

run: build-web
	cd server && go run .

# Run Air and Vite directly so each can handle shutdown for its children.
dev: build-web
	@bash -c '\
		cleanup() { \
			trap "" INT TERM; \
			kill -TERM -- $$air_pid $$vite_pid 2>/dev/null || :; \
			wait; \
		}; \
		trap cleanup EXIT; trap "exit 0" INT TERM; \
		(cd server && VITE_DEV_URL=http://localhost:5173 exec "$(AIR)") & air_pid=$$!; \
		(cd server/web && exec ./node_modules/.bin/vite) <&0 & vite_pid=$$!; \
		wait'

test: build-web
	cd server && go test -v ./... && go vet ./...

version:
	@$(VERSION_COMMAND)

bump:
	@case "$$BUMP" in \
		patch|minor|major) cd server/web && pnpm version "$$BUMP" --no-git-tag-version --no-git-checks ;; \
		*) echo "Usage: make bump BUMP=patch|minor|major" >&2; exit 1 ;; \
	esac

docker-build docker-push:
	@set -e; version=$$($(VERSION_COMMAND)); image="$(IMAGE):$$version"; \
	docker build --load --build-arg VERSION="$$version" --tag "$$image" server; \
	if [ "$@" = docker-push ]; then \
		docker push "$$image"; \
		docker tag "$$image" "$(IMAGE):latest"; \
		docker push "$(IMAGE):latest"; \
	fi
