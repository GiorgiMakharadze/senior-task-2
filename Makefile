.PHONY: help lint format test

help:
	@echo "Available commands:"
	@echo "  make lint        - Run linter on the codebase"
	@echo "  make format      - Format the code"
	@echo "  make test        - Run tests"

lint: format
	@go vet ./...

format:
	@gofmt -s -w .

test:
	@go test -v ./...
