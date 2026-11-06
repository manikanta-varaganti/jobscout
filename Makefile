# jobscout developer tasks. Run `make help`.
BINARY  := jobscout
PKG     := ./cmd/jobscout
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help build run test race cover lint vet fmt fmt-check tidy vuln docs-check check clean release-snapshot

help: ## list targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-17s %s\n", $$1, $$2}'

build: ## build ./bin/jobscout for this machine
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

run: build ## build and start the dashboard
	./bin/$(BINARY) serve

test: ## run unit tests
	go test ./...

race: ## run tests with the race detector
	go test -race -count=1 ./...

cover: ## coverage report in coverage.html
	go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out -o coverage.html

vet: ## go vet
	go vet ./...

lint: ## golangci-lint (install: https://golangci-lint.run)
	golangci-lint run

fmt: ## format code
	gofmt -s -w .

fmt-check: ## fail if code is not gofmt-clean
	@test -z "$$(gofmt -s -l .)" || (echo "run 'make fmt':"; gofmt -s -l .; exit 1)

tidy: ## verify go.mod/go.sum are tidy
	go mod tidy && git diff --exit-code go.mod go.sum

vuln: ## scan dependencies for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

docs-check: ## verify every relative link and #anchor in the Markdown docs
	python3 scripts/check_docs.py

check: fmt-check vet race docs-check ## everything CI runs locally

release-snapshot: ## build all platform archives into ./dist without publishing
	goreleaser release --snapshot --clean

clean: ## remove build output
	rm -rf bin dist coverage.out coverage.html
