GO ?= go
BIN_DIR ?= bin

.PHONY: all build test clean verify

all: build

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/pi ./cmd/pi
	$(GO) build -o $(BIN_DIR)/pi-index ./cmd/pi-index

test:
	$(GO) test ./...

verify: build test
	$(BIN_DIR)/pi help
	$(BIN_DIR)/pi-index -h

clean:
	rm -rf $(BIN_DIR)
