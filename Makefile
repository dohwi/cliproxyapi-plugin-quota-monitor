SHELL := /bin/bash

# Library extension and loader flags follow the build host (GOOS). Override with
# `make build GOOS=...` only for a matching native toolchain: the plugin is a CGO
# c-shared library and must be built natively per platform.
GOOS ?= $(shell go env GOOS)
ifeq ($(GOOS),windows)
LIB_EXT := dll
SMOKE_EXE := .exe
SMOKE_LIBS :=
else ifeq ($(GOOS),darwin)
LIB_EXT := dylib
SMOKE_EXE :=
SMOKE_LIBS :=
else
LIB_EXT := so
SMOKE_EXE :=
SMOKE_LIBS := -ldl
endif
LIB := artifacts/quota-monitor.$(LIB_EXT)
SMOKE := artifacts/native-smoke$(SMOKE_EXE)
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo "shasum -a 256")

.PHONY: verify-i18n release-check verify build native-smoke clean verify-layout

verify:
	go test -race -count=1 -timeout=120s ./...
	go vet ./...

build:
	mkdir -p artifacts
	go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o $(LIB) .
	$(SHA256) $(LIB)

native-smoke:
	scripts/build-native.sh


verify-layout:
	node scripts/test-quota-dashboard-layout.cjs

verify-i18n:
	node scripts/test-quota-dashboard-language.cjs

# Usage: make release-check TAG=v0.1.0
release-check:
	./scripts/check-release-version.sh $(TAG)

clean:
	rm -rf artifacts dist
