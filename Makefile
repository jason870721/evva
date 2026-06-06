BINARY_NAME=evva
BIN_DIR=bin
CMD_DIR=./cmd/evva

# Build-time version injection.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")

LDFLAGS = -s -w \
	-X github.com/johnny1110/evva/pkg/config.Version=$(VERSION) \
	-X github.com/johnny1110/evva/pkg/config.CommitSHA=$(COMMIT) \
	-X github.com/johnny1110/evva/pkg/config.BuildDate=$(DATE)

# Install location: defaults to Go's standard binary directory
# ($GOBIN, or $GOPATH/bin when GOBIN is unset). Both are typically
# already on a Go developer's PATH so `evva` works from any folder
# right after `make install`. Override for a system-wide install:
#
#   sudo make install PREFIX=/usr/local/bin
#   make install PREFIX=$$HOME/.local/bin
#
GOBIN_DIR := $(shell go env GOBIN)
ifeq ($(GOBIN_DIR),)
GOBIN_DIR := $(shell go env GOPATH)/bin
endif
PREFIX ?= $(GOBIN_DIR)

.PHONY: all build run test vet fmt tidy clean lint install uninstall depcheck

all: fmt vet depcheck test build

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_DIR)

# install builds and drops the binary into PREFIX. The runtime config
# dir ($HOME/.evva/) bootstraps itself on first launch — neither
# evva-config.yml nor .env need to be hand-created.
install: build
	@mkdir -p $(PREFIX)
	@install -m 0755 $(BIN_DIR)/$(BINARY_NAME) $(PREFIX)/$(BINARY_NAME)
	@echo ""
	@echo "  installed $(PREFIX)/$(BINARY_NAME)"
	@echo ""
	@case ":$$PATH:" in \
		*":$(PREFIX):"*) echo "  $(PREFIX) is on your PATH — run 'evva' from any directory." ;; \
		*) echo "  $(PREFIX) is NOT on your PATH. Add this line to your shell rc:"; \
		   echo "       export PATH=\"$(PREFIX):\$$PATH\""; \
		   echo "  then 'source' it (or open a new terminal)." ;; \
	esac
	@echo ""
	@echo "  On first launch evva auto-creates:"
	@echo "    $$HOME/.evva/config/evva-config.yml   (user-tunable settings)"
	@echo "  Optional, only needed for deploy/log overrides:"
	@echo "    $$HOME/.evva/.env                     (APP_ENV, LOG_LEVEL, ...)"
	@echo ""
	@echo "  Set API keys via /config inside the TUI or hand-edit the YAML."

uninstall:
	@rm -f $(PREFIX)/$(BINARY_NAME)
	@echo "  removed $(PREFIX)/$(BINARY_NAME)"
	@echo "  (your $$HOME/.evva/ config dir was left intact — delete it manually if you want a clean wipe.)"

run:
	go run $(CMD_DIR)

test:
	go test -race -cover ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

lint:
	golangci-lint run ./...

# depcheck enforces the Veronica multi-agent oracle: internal/swarm must
# import only pkg/* (+ internal/swarm). See scripts/depcheck.sh.
depcheck:
	@bash scripts/depcheck.sh

clean:
	rm -rf $(BIN_DIR)
