.PHONY: build test lint fmt clean run

BIN := server

build:
	go build -o $(BIN) ./cmd/server

test:
	go test -v -count=1 ./...

lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; fi

fmt:
	gofmt -w .

clean:
	rm -f $(BIN)
	rm -rf data/

run: build
	./$(BIN)
