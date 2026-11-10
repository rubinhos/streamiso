#!/bin/sh
# Formats the imports, then vets and tests everything. Run it in Docker from the catalog folder, so nothing
# needs to be installed:
#   docker run --rm -v "$PWD:/src" -w /src golang:1.24-alpine sh tools/check.sh
set -e
export GOFLAGS=-buildvcs=false
[ -x /go/bin/goimports ] || go install golang.org/x/tools/cmd/goimports@v0.24.0 >/dev/null 2>&1
/go/bin/goimports -w $(find . -name '*.go')
unformatted=$(gofmt -l .)
[ -z "$unformatted" ] || { echo "Not formatted: $unformatted"; exit 1; }
go vet ./...
go test ./...
