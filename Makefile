.PHONY: run test check contract integration test-db
run:
	go run ./cmd/life-api
test:
	go test ./...
contract:
	go run ./cmd/contract > api/openapi.json
check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }
	go test ./...
	go vet ./...
	go build ./...
	@contract=$$(mktemp); trap 'rm -f "$$contract"' EXIT; go run ./cmd/contract > "$$contract" && diff -u api/openapi.json "$$contract"
test-db:
	docker compose -f compose.test.yaml up -d --wait postgres

