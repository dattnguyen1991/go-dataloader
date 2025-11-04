lint:
	golangci-lint run
.PHONY: lint

test:
	go test ./... && \
	go test -bench=. ./...
.PHONY: test