#!/usr/bin/env bash
# Cross-compiles the nabu CLI for every supported platform into dist/.
#
# The whole CLI, daemon included, is pure Go with no cgo, so this needs no
# toolchain beyond Go itself: a Mac build can be produced from Windows and a
# Windows build from a Mac.
set -euo pipefail

cd "$(dirname "$0")/.."
version="${1:-dev}"
out="dist"
rm -rf "$out"
mkdir -p "$out"

targets=(
  "darwin arm64"   # Apple silicon
  "darwin amd64"   # Intel Macs
  "linux amd64"
  "linux arm64"
  "windows amd64"
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  name="nabu_${goos}_${goarch}"
  [ "$goos" = "windows" ] && name="$name.exe"

  # CGO off keeps the binary static and the cross-build honest: with it on,
  # these would silently need a C toolchain per platform.
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=$version" \
    -o "$out/$name" ./cmd/nabu

  echo "built $out/$name"
done

echo
echo "release $version in $out/"
