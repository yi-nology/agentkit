# agentkit 质量门禁——发布前跑 `make check`（与 tag 制发布流程配套）。

GO ?= go

.PHONY: all build vet fmt lint test race vuln check

all: check

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w .

lint:
	golangci-lint run ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vuln:
	govulncheck ./...

check: build vet lint test vuln
