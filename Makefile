.PHONY: run build fmt vet test tidy

run:
	go run ./cmd/brain

build:
	go build -o bin/brain ./cmd/brain

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test -race ./...

tidy:
	go mod tidy
