.PHONY: test coverage build lint

test:
	go test ./... -race

coverage:
	go test ./... -coverprofile=cover.out -covermode=atomic
	@go tool cover -func=cover.out | tail -1
	@go tool cover -func=cover.out | tail -1 | awk '{gsub(/%/,""); if ($$3+0 < 80.0) {print "FAIL: coverage below 80%"; exit 1}}'

build:
	go build ./cmd/mockwave/

lint:
	golangci-lint run ./...
