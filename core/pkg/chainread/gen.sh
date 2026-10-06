#!/usr/bin/env bash
# Regenerates queries.binpb: the descriptors of every orama module's Query
# service, with their imports, from chain/proto. core/ links no chain code, so
# the embedded descriptors are how `orama chain` encodes a gRPC query request
# and decodes its response without a generated Go type.
#
# OUT overrides the output path (the freshness test writes to a temp file).
# Needs protoc and go on PATH. Run from anywhere; it resolves the proto
# dependencies at the versions chain/go.mod pins.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chain_dir="$here/../../../chain"

mod_dir() { (cd "$chain_dir" && go list -m -f '{{.Dir}}' "$1"); }
sdk_mod="$(mod_dir github.com/cosmos/cosmos-sdk)"
gogo_mod="$(mod_dir github.com/cosmos/gogoproto)"
cproto_mod="$(mod_dir github.com/cosmos/cosmos-proto)"

protoc \
  -I "$chain_dir/proto" -I "$sdk_mod/proto" -I "$gogo_mod" -I "$cproto_mod/proto" \
  --include_imports --descriptor_set_out="${OUT:-$here/queries.binpb}" \
  "$chain_dir"/proto/orama/*/v1/query.proto
