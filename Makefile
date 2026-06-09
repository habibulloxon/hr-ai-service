BINARY := bin/server

.PHONY: fmt vet lint test build run tidy docker clean

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	golangci-lint run

test:
	go test ./...

build:
	go build -o $(BINARY) ./cmd/server

run:
	go run ./cmd/server

tidy:
	go mod tidy

docker:
	docker build -t hr-microservice .

clean:
	rm -rf bin
