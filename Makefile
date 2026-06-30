.PHONY: build run fmt

build:
	go build -o bin/goss ./cmd/goss

run:
	go run ./cmd/goss

fmt:
	go fmt ./...
