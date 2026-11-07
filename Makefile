.PHONY: build test install

build:
	go build -o bin/flights-mcp ./cmd/flights-mcp

test:
	go test ./...

# Register the server with Claude Code for every project.
install: build
	claude mcp add --scope user flights -- $(CURDIR)/bin/flights-mcp
