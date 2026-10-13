#!/usr/bin/env bash
# Run the demo's tests: the functions' logic under `go test` with an in-memory
# host, and the web page's helpers under Node. Neither needs a network.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

go vet ./...
go test ./...
node --test test/app.test.mjs
