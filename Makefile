BINARY := bin/awsfilter

.PHONY: build test fmt vet

build:
	go build -o $(BINARY) ./cmd/awsfilter

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal

vet:
	go vet ./...
