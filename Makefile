.PHONY: run test

run:
	go run ./cmd/server

test:
	go test ./...

# API gateway has no database; migrations run in the owning service.
