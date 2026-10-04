#!/bin/sh
# Builds a standalone binary per platform into dist/, so nobody needs Go to run
# it. CGO_ENABLED=0 is explicit - if a dependency ever pulls in cgo,
# cross-compiling breaks here rather than with a linker error. -s -w strips
# debug info, 11 MB down to 7.8.
set -e

# First argument, else the current git tag, else "dev".
version=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}

out=dist
rm -rf "$out"
mkdir -p "$out"

# GOOS GOARCH, then the name to ship it under. No darwin/amd64 - one line to add
# if anyone asks.
cat <<'TARGETS' | while read goos goarch name; do
windows amd64 trackorganiser-windows-amd64.exe
windows arm64 trackorganiser-windows-arm64.exe
linux   amd64 trackorganiser-linux-amd64
linux   arm64 trackorganiser-linux-arm64
darwin  arm64 trackorganiser-macos-arm64
TARGETS
	GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 \
		go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$out/$name" .
	echo "  $(du -h "$out/$name" | cut -f1)	$name"
done

echo "version: $version"
