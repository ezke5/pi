GO ?= go
BIN_DIR ?= bin

.PHONY: all build pi an pi-index test clean verify

all: build

build: pi an pi-index

pi:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/pi ./cmd/pi

an:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/an ./cmd/an

pi-index:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/pi-index ./cmd/pi-index

test:
	$(GO) test ./...

verify: build test
	$(BIN_DIR)/pi help
	$(BIN_DIR)/an help
	$(BIN_DIR)/pi-index -h

clean:
	rm -rf $(BIN_DIR)
