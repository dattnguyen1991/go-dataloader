lint:
	golangci-lint run
.PHONY: lint

test:
	go test ./...
.PHONY: lint