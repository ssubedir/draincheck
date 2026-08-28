BINARY := draincheck
RUNTIME ?= docker
E2E_REPORT_DIR ?= reports/conformance-$(RUNTIME)
DOGFOOD_IMAGE ?= draincheck-good:dogfood
DOGFOOD_REPORT_DIR ?= reports/dogfood
PILOT_CASE ?= all
PILOT_REPORT_DIR ?= reports/pilot-$(RUNTIME)
EXTERNAL_PILOT_CONFIG ?= testdata/pilot/external/go-httpbin/draincheck.yaml
EXTERNAL_PILOT_REPORT_DIR ?= reports/external-pilots/go-httpbin
RELEASE_OUTPUT ?= dist
RELEASE_VERSION ?= v0.0.0-dev
RELEASE_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
RELEASE_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

.PHONY: build test race format-check lint vulncheck check schema fixtures e2e dogfood pilot external-pilot release-dry-run clean

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/draincheck

test:
	go test ./...

race:
	go test -race ./...

format-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "$$unformatted"; \
		echo "run gofmt on the files listed above"; \
		exit 1; \
	fi

lint:
	go vet ./...
	go tool staticcheck ./...

vulncheck:
	go tool govulncheck ./...

check: format-check
	go mod verify
	go vet ./...
	go tool staticcheck ./...
	go test -race ./...
	go tool govulncheck ./...
	go build ./...

schema:
	go run ./cmd/draincheck schema --output schema/draincheck.schema.json

fixtures:
	$(RUNTIME) build -f testdata/services/good-http/Dockerfile -t draincheck-good:local testdata/services
	$(RUNTIME) build -f testdata/services/ignores-term/Dockerfile -t draincheck-ignores:local testdata/services

e2e:
	DRAINCHECK_E2E_RUNTIME=$(RUNTIME) DRAINCHECK_E2E_REPORT_DIR=$(E2E_REPORT_DIR) go test -v -count=1 ./testdata/e2e

dogfood: build
	mkdir -p $(DOGFOOD_REPORT_DIR)
	$(RUNTIME) build -f testdata/services/good-http/Dockerfile -t $(DOGFOOD_IMAGE) testdata/services
	./bin/$(BINARY) verify $(DOGFOOD_IMAGE) \
		--runtime $(RUNTIME) \
		--config testdata/services/good-http/draincheck.yaml \
		--report-json $(DOGFOOD_REPORT_DIR)/draincheck.json \
		--report-junit $(DOGFOOD_REPORT_DIR)/draincheck.xml \
		--debug-bundle $(DOGFOOD_REPORT_DIR)/draincheck-debug.zip \
		--no-color

pilot:
	DRAINCHECK_PILOT_RUNTIME=$(RUNTIME) \
	DRAINCHECK_PILOT_CASE=$(PILOT_CASE) \
	DRAINCHECK_PILOT_REPORT_DIR=$(PILOT_REPORT_DIR) \
	go test -v -count=1 ./testdata/pilot

external-pilot: build
	./bin/$(BINARY) verify \
		--runtime $(RUNTIME) \
		--pull missing \
		--config $(EXTERNAL_PILOT_CONFIG) \
		--report-json $(EXTERNAL_PILOT_REPORT_DIR)/draincheck.json \
		--report-junit $(EXTERNAL_PILOT_REPORT_DIR)/draincheck.xml \
		--debug-bundle $(EXTERNAL_PILOT_REPORT_DIR)/draincheck-debug.zip \
		--no-color

release-dry-run:
	go run ./tools/release package \
		--version "$(RELEASE_VERSION)" \
		--commit "$(RELEASE_COMMIT)" \
		--date "$(RELEASE_DATE)" \
		--output "$(RELEASE_OUTPUT)"
	go run ./tools/release checksums --output "$(RELEASE_OUTPUT)"

clean:
	go clean
