#!/usr/bin/env bash
set -euo pipefail
image=${1:?image required}
version=${2:?version required}
test "$(docker run --rm "$image" version)" = "gopatch $version"
docker run --rm "$image" --help > /dev/null
mkdir -p .tmp
work=$(mktemp -d .tmp/image-smoke.XXXXXX)
trap 'rm -rf "$work"' EXIT
cp testdata/release-smoke/* "$work/"
docker run --rm -v "$PWD/$work:/workspace" "$image" move func source.go:Hello moved.go --dry-run
cmp testdata/release-smoke/source.go "$work/source.go"
test ! -e "$work/moved.go"
docker run --rm -v "$PWD/$work:/workspace" "$image" move func source.go:Hello moved.go
test -f "$work/moved.go"
docker run --rm -v "$PWD/$work:/workspace" --entrypoint go "$image" test ./...
