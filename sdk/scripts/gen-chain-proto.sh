#!/usr/bin/env bash
# Regenerates sdk/src/chain/gen from the chain's protobuf sources.
#
# The inputs are the chain's own protos (chain/proto) and the exact dependency
# versions chain/go.mod pins (cosmos-sdk, gogoproto, cosmos-proto, wasmd), read
# from the Go module cache. The generator is ts-proto, pinned in
# sdk/package.json. OUT_DIR overrides the output directory. Needs protoc and go
# on PATH; run `pnpm install` first.
set -euo pipefail

sdk_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chain_dir="$sdk_dir/../chain"
out_dir="${OUT_DIR:-$sdk_dir/src/chain/gen}"

mod_dir() { (cd "$chain_dir" && go list -m -f '{{.Dir}}' "$1"); }

sdk_mod="$(mod_dir github.com/cosmos/cosmos-sdk)"
gogo_mod="$(mod_dir github.com/cosmos/gogoproto)"
cproto_mod="$(mod_dir github.com/cosmos/cosmos-proto)"
wasmd_mod="$(mod_dir github.com/CosmWasm/wasmd)"

rm -rf "$out_dir"
mkdir -p "$out_dir"

# Every Msg service of the chain, plus the SDK and CosmWasm messages a wallet
# signs. Query messages are not generated: the read side is JSON over REST.
protos=(
  "$chain_dir"/proto/orama/*/v1/tx.proto
  "$sdk_mod/proto/cosmos/tx/v1beta1/tx.proto"
  "$sdk_mod/proto/cosmos/bank/v1beta1/tx.proto"
  "$sdk_mod/proto/cosmos/staking/v1beta1/tx.proto"
  "$sdk_mod/proto/cosmos/slashing/v1beta1/tx.proto"
  "$sdk_mod/proto/cosmos/distribution/v1beta1/tx.proto"
  "$sdk_mod/proto/cosmos/crypto/secp256k1/keys.proto"
  "$wasmd_mod/proto/cosmwasm/wasm/v1/tx.proto"
)

protoc \
  -I "$chain_dir/proto" -I "$sdk_mod/proto" -I "$gogo_mod" \
  -I "$cproto_mod/proto" -I "$wasmd_mod/proto" \
  --plugin="protoc-gen-ts_proto=$sdk_dir/node_modules/.bin/protoc-gen-ts_proto" \
  --ts_proto_out="$out_dir" \
  --ts_proto_opt=esModuleInterop=true,forceLong=bigint,outputServices=false,outputClientImpl=false,importSuffix=.js \
  "${protos[@]}"

echo "generated $(find "$out_dir" -name '*.ts' | wc -l | tr -d ' ') files in $out_dir"
