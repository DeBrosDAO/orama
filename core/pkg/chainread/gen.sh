#!/usr/bin/env bash
# Regenerates queries.binpb: the descriptors of every orama module's Query
# service plus the cosmos-sdk and wasmd Query services a wallet reads (bank,
# auth, staking, distribution, cosmwasm.wasm), with their imports. core/ links
# no chain code, so the embedded descriptors are how `orama chain` and the
# gateway's /v1/chain/query/ route encode a gRPC query request and decode its
# response without a generated Go type. The Orama protos come from chain/proto;
# the others from the cosmos-sdk and wasmd versions chain/go.mod pins.
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
comet_mod="$(mod_dir github.com/cometbft/cometbft)"
wasm_mod="$(mod_dir github.com/CosmWasm/wasmd)"
googleapis_dir="$(mod_dir github.com/grpc-ecosystem/grpc-gateway)/third_party/googleapis"

protoc \
  -I "$chain_dir/proto" -I "$sdk_mod/proto" -I "$gogo_mod" -I "$cproto_mod/proto" \
  -I "$comet_mod/proto" -I "$wasm_mod/proto" -I "$googleapis_dir" \
  --include_imports --descriptor_set_out="${OUT:-$here/queries.binpb}" \
  "$chain_dir"/proto/orama/*/v1/query.proto \
  "$sdk_mod"/proto/cosmos/{bank,auth,staking,distribution}/v1beta1/query.proto \
  "$wasm_mod"/proto/cosmwasm/wasm/v1/query.proto
