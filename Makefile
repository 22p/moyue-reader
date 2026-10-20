BINARY := bin/moyue

.PHONY: run build test fmt

run:
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

test:
	go test ./...

fmt:
	gofmt -w .
