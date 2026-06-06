.PHONY: test coverage build lint release-local test-integration itest-up itest-down

test:
	go test ./... -race

# Bring up store dependencies (DynamoDB Local, MongoDB) for integration tests.
itest-up:
	docker compose -f docker-compose.test.yml up -d --wait

# Tear down store dependencies and remove volumes.
itest-down:
	docker compose -f docker-compose.test.yml down -v

# Run the integration-tagged tests against containerized dependencies.
# Spins deps up, runs the suite with -race, tears deps down even on failure.
test-integration:
	docker compose -f docker-compose.test.yml up -d --wait
	DYNAMO_TEST_ENDPOINT=http://localhost:8000 \
	MONGO_TEST_URI=mongodb://localhost:27017 \
		go test -tags integration -race ./... ; \
	status=$$? ; \
	docker compose -f docker-compose.test.yml down -v ; \
	exit $$status

coverage:
	go test ./... -coverprofile=cover.out -covermode=atomic
	@go tool cover -func=cover.out | tail -1
	@go tool cover -func=cover.out | tail -1 | awk '{gsub(/%/,""); if ($$3+0 < 80.0) {print "FAIL: coverage below 80%"; exit 1}}'

build:
	go build ./cmd/mockwave/

lint:
	golangci-lint run ./...

# Build all release targets locally into dist/ (mirrors what CI does)
release-local:
	mkdir -p dist
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-darwin-amd64  ./cmd/mockwave/
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-darwin-arm64  ./cmd/mockwave/
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-linux-amd64   ./cmd/mockwave/
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-linux-arm64   ./cmd/mockwave/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-windows-amd64.exe ./cmd/mockwave/
	shasum -a 256 dist/* > dist/checksums.txt
	@echo "✓ All binaries built in dist/"
	@cat dist/checksums.txt
