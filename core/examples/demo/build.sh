#!/usr/bin/env bash
# Build the demo functions to WASM with TinyGo, the way `orama function build`
# does (tinygo build -target wasi in the function's directory), and check that the
# output is a WASM module. The files land next to each function.wasm, where
# `orama function deploy` looks for them.
#
# Usage: ./build.sh            build every function
#        ./build.sh visits     build one
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v tinygo >/dev/null 2>&1; then
  echo "tinygo is not installed: https://tinygo.org/getting-started/install/ (macOS: brew install tinygo)" >&2
  exit 1
fi

if [ "$#" -gt 0 ]; then
  names=("$@")
else
  names=()
  for dir in "$here"/functions/*/; do
    names+=("$(basename "$dir")")
  done
fi

for name in "${names[@]}"; do
  dir="$here/functions/$name"
  if [ ! -f "$dir/function.go" ] || [ ! -f "$dir/function.yaml" ]; then
    echo "functions/$name has no function.go and function.yaml" >&2
    exit 1
  fi
  echo "Building $name..."
  (cd "$dir" && tinygo build -o function.wasm -target wasi .)
  # A WASM module starts with \0asm.
  if [ "$(head -c 4 "$dir/function.wasm" | od -An -tx1 | tr -d ' \n')" != "0061736d" ]; then
    echo "functions/$name/function.wasm is not a WASM module" >&2
    exit 1
  fi
  echo "  -> functions/$name/function.wasm ($(wc -c < "$dir/function.wasm" | tr -d ' ') bytes)"
done
