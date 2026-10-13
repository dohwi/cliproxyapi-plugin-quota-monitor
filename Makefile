SHELL := /bin/bash

.PHONY: verify-i18n release-check verify build native-smoke clean verify-layout

verify:
	go test -race -count=1 -timeout=120s ./...
	go vet ./...

build:
	mkdir -p artifacts
	go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o artifacts/quota-monitor.so .
	sha256sum artifacts/quota-monitor.so

native-smoke: build
	gcc -O2 -Wall -Wextra -Werror -o /tmp/quota-monitor-native-smoke verification/native_smoke.c -ldl
	/tmp/quota-monitor-native-smoke artifacts/quota-monitor.so

verify-layout:
	node scripts/test-quota-dashboard-layout.cjs

verify-i18n:
	node scripts/test-quota-dashboard-language.cjs

# Usage: make release-check TAG=v0.1.0
release-check:
	./scripts/check-release-version.sh $(TAG)

clean:
	rm -rf artifacts
	rm -f /tmp/quota-monitor-native-smoke
