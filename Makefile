VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/farshadfighter/taktaplus/internal/buildinfo
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: all web build test lint run db-up db-down docker clean

all: web build

web:
	cd web && npm ci && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/taktaplus ./cmd/taktaplus

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	cd web && npm run typecheck

run: build
	set -a; . ./.env; set +a; ./bin/taktaplus api

db-up:
	docker compose -f deploy/docker-compose.yml up -d postgres redis clickhouse

db-down:
	docker compose -f deploy/docker-compose.yml down

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t taktaplus:$(VERSION) .

clean:
	rm -rf bin web/dist/app
