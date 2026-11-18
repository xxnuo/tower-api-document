.PHONY: install build-web build run dev test

AIR := $(shell command -v air 2>/dev/null || echo $(HOME)/go/bin/air)

install:
	cd oauth-server/web && pnpm install

build-web:
	cd oauth-server/web && pnpm build

build: build-web
	cd oauth-server && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/tower-oauth .
	@if command -v upx >/dev/null 2>&1; then upx --best --lzma -q oauth-server/bin/tower-oauth; fi

run: build-web
	cd oauth-server && go run .

dev:
	@bash -c 'trap "kill 0" EXIT INT TERM; (cd oauth-server && VITE_DEV_URL=http://localhost:5173 "$(AIR)") & (cd oauth-server/web && pnpm dev) & wait'

test: build-web
	cd oauth-server && go test -v ./... && go vet ./...
