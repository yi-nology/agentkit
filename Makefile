# agentkit 质量门禁——发布前跑 `make check`（与 tag 制发布流程配套）。
# v0.10.13 起 check 纳入 race（并发原语持有方式历经多轮重构，race 全绿后收编门禁）。

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

check: build vet lint test race vuln
