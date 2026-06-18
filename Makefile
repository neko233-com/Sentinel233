VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS  = -s -w \
  -X github.com/neko233-com/Sentinel233/internal/version.Version=$(VERSION) \
  -X github.com/neko233-com/Sentinel233/internal/version.Commit=$(COMMIT) \
  -X github.com/neko233-com/Sentinel233/internal/version.Date=$(DATE)

.PHONY: build test test-race lint run-server run-agent clean smoke docker-e2e docker-e2e-local

build:
	go build -ldflags="$(LDFLAGS)" -o bin/sentinel233-server.exe ./cmd/sentinel233-server
	go build -ldflags="$(LDFLAGS)" -o bin/sentinel233.exe ./cmd/sentinel233
	go build -ldflags="$(LDFLAGS)" -o bin/sentinel233-agent.exe ./cmd/sentinel233-agent

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o bin/sentinel233-server ./cmd/sentinel233-server
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o bin/sentinel233 ./cmd/sentinel233
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o bin/sentinel233-agent ./cmd/sentinel233-agent

test:
	go test ./... -count=1 -timeout=120s

test-race:
	go test ./... -count=1 -race -timeout=180s

test-verbose:
	go test ./... -count=1 -v -timeout=120s

lint:
	golangci-lint run --timeout=5m

vet:
	go vet ./...

run-server:
	go run ./cmd/sentinel233-server -addr :23390

run-agent:
	go run ./cmd/sentinel233-agent -addr :23391 -server http://localhost:23390

smoke: build
	@echo "=== Smoke Test ==="
	@./bin/sentinel233-server.exe -version
	@./bin/sentinel233.exe -version
	@./bin/sentinel233-agent.exe -version
	@echo "=== Smoke OK ==="

clean:
	rm -rf bin/ data/

install:
	go install -ldflags="$(LDFLAGS)" ./cmd/sentinel233-server
	go install -ldflags="$(LDFLAGS)" ./cmd/sentinel233
	go install -ldflags="$(LDFLAGS)" ./cmd/sentinel233-agent

docker-build:
	docker build -t sentinel233-server:latest .

docker-run:
	docker compose up -d

docker-stop:
	docker compose down

docker-e2e:
	pwsh ./scripts/docker-grafana-replacement-e2e.ps1

docker-e2e-local:
	pwsh ./scripts/docker-grafana-replacement-e2e.ps1 -UseLocalBinary
