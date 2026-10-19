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
# resources regenerates the Windows version-info and manifest that are linked
# into the Windows binaries. They are committed, so a plain build works without
# this; rerun it when versioninfo.json changes.
#
# They are not cosmetic. An unsigned Windows exe with no version metadata and no
# application manifest is what Windows Defender's ML heuristic flags as
# Trojan:Win32/Bearfoos.A!ml, and it quarantines the binary — the file vanishes
# from disk mid-use. Verified with Defender's own scanner: the build without
# resources reports "found 1 threats", the build with them reports "found no
# threats".
resources:
	@command -v goversioninfo >/dev/null 2>&1 || \
	  go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
	cd cmd/zedrun && goversioninfo -64 -o resource_windows_amd64.syso versioninfo.json
	cd cmd/zedrun && goversioninfo -arm -o resource_windows_arm64.syso versioninfo.json

.PHONY: resources

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
