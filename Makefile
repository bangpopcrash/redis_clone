.PHONY: build run test test-race fuzz lint fmt vet clean

BINARY := bin/redis-server

build:
	go build -o $(BINARY) ./cmd/redis-server

run: build
	./$(BINARY)

test:
	go test ./...

test-race:
	go test -race ./...

fuzz:
	go test -fuzz=FuzzDecode -fuzztime=30s ./internal/resp/...

lint:
	gofmt -l .
	go vet ./...
	golangci-lint run ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin/
