GO ?= go
BIN ?= zedrun-go
DIST ?= dist

.PHONY: all build test vet fmt tidy dist clean

all: vet test build

build:
	$(GO) build -trimpath -o $(BIN) ./cmd/zedrun

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

# Cross-compile the release assets. CGO is off so the binaries are static.
dist:
	@mkdir -p $(DIST)
	@set -eu; \
	for os in linux darwin windows; do \
	  for arch in amd64 arm64; do \
	    ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	    echo "building $(DIST)/$(BIN)-$$os-$$arch$$ext"; \
	    GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
	      $(GO) build -trimpath -ldflags "-s -w" \
	      -o "$(DIST)/$(BIN)-$$os-$$arch$$ext" ./cmd/zedrun; \
	  done; \
	done

clean:
	rm -rf $(BIN) $(DIST)
