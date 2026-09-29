#!/usr/bin/env bash
# Reproducible build of the genesis standard contracts (docs/CHAIN.md, "Genesis standard contracts").
#
# Every contract is compiled from a pinned upstream git commit, with a pinned Rust toolchain, and
# post-processed the way the official cosmwasm/optimizer does (wasm-opt -Os --signext-lowering).
# No prebuilt .wasm is ever downloaded. The pins live in manifest.json.
#
# Usage:
#   build.sh verify   build every contract and fail if a sha256 differs from manifest.json
#   build.sh update   build every contract, write wasm/*.wasm and the sha256 fields of manifest.json
#
# Needs: git, python3, rustup with the pinned toolchain and the wasm32-unknown-unknown target
#   (rustup toolchain install 1.81.0 && rustup target add wasm32-unknown-unknown --toolchain 1.81.0),
#   and the wasm-opt (binaryen) version named in manifest.json. Set ALLOW_TOOL_DRIFT=1 to build with
#   another wasm-opt; the hashes will then almost certainly differ and verify will say so.
#
# Why Rust 1.81: wasmvm v3.0.7 rejects modules that use bulk-memory, and rustc 1.87 and later emit it
# from the precompiled standard library. 1.81 emits neither bulk-memory nor reference-types.
set -euo pipefail

mode="${1:-}"
case "$mode" in
verify | update) ;;
*)
	echo "usage: $0 verify|update" >&2
	exit 2
	;;
esac

here="$(cd "$(dirname "$0")" && pwd)"
manifest="$here/manifest.json"
work="${WORK_DIR:-$(mktemp -d)}"
trap '[ -n "${WORK_DIR:-}" ] || rm -rf "$work"' EXIT

field() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2], {"d": d}))' "$manifest" "$1"; }

toolchain="$(field 'd["toolchain"]["rust"]')"
target="$(field 'd["toolchain"]["target"]')"
want_wasm_opt="$(field 'd["toolchain"]["wasm_opt"]')"
wasm_opt_flags="$(field 'd["toolchain"]["wasm_opt_flags"]')"

have_wasm_opt="$(wasm-opt --version)"
if [ "$have_wasm_opt" != "$want_wasm_opt" ] && [ "${ALLOW_TOOL_DRIFT:-0}" != "1" ]; then
	echo "wasm-opt is '$have_wasm_opt', manifest pins '$want_wasm_opt' (ALLOW_TOOL_DRIFT=1 to override)" >&2
	exit 1
fi
if ! rustup run "$toolchain" rustc --version >/dev/null 2>&1; then
	echo "rust toolchain $toolchain is not installed: rustup toolchain install $toolchain" >&2
	exit 1
fi

count="$(field 'len(d["contracts"])')"
failed=0
results=()
for i in $(seq 0 $((count - 1))); do
	name="$(field "d['contracts'][$i]['name']")"
	repo="$(field "d['contracts'][$i]['source']['repo']")"
	commit="$(field "d['contracts'][$i]['source']['commit']")"
	package="$(field "d['contracts'][$i]['source']['package']")"
	artifact="$(field "d['contracts'][$i]['artifact']")"
	patch="$(field "d['contracts'][$i]['source'].get('patch') or ''")"
	ignore_msrv="$(field "'--ignore-rust-version' if d['contracts'][$i]['source'].get('ignore_rust_version') else ''")"
	want="$(field "d['contracts'][$i].get('sha256') or ''")"

	src="$work/$name-src"
	echo "==> $name: fetching $repo @ $commit"
	git init -q "$src"
	git -C "$src" remote add origin "$repo"
	git -C "$src" fetch -q --depth 1 origin "$commit"
	git -C "$src" checkout -q FETCH_HEAD
	got_commit="$(git -C "$src" rev-parse HEAD)"
	if [ "$got_commit" != "$commit" ]; then
		echo "$name: fetched $got_commit, manifest pins $commit" >&2
		exit 1
	fi
	if [ -n "$patch" ]; then
		echo "==> $name: applying $patch"
		git -C "$src" apply "$here/$patch"
	fi

	echo "==> $name: cargo build ($toolchain, $package)"
	cargo_home="${CARGO_HOME:-$HOME/.cargo}"
	(
		cd "$src"
		export RUSTFLAGS="-C link-arg=-s --remap-path-prefix=$src=/src --remap-path-prefix=$cargo_home=/cargo"
		# shellcheck disable=SC2086
		rustup run "$toolchain" cargo build --release --lib --target "$target" --locked $ignore_msrv -p "$package"
	)
	built="$src/target/$target/release/$(echo "$package" | tr '-' '_').wasm"
	out="$work/$artifact"
	mkdir -p "$(dirname "$out")"
	# shellcheck disable=SC2086
	wasm-opt $wasm_opt_flags "$built" -o "$out"
	sum="$(shasum -a 256 "$out" | cut -d' ' -f1)"
	echo "==> $name: sha256 $sum"
	results+=("$name=$sum")

	if [ "$mode" = "verify" ]; then
		if [ "$sum" != "$want" ]; then
			echo "$name: MISMATCH built $sum, manifest $want" >&2
			failed=1
		fi
	else
		mkdir -p "$here/$(dirname "$artifact")"
		cp "$out" "$here/$artifact"
	fi
done

if [ "$mode" = "update" ]; then
	python3 - "$manifest" "${results[@]}" <<'PY'
import json, sys
path = sys.argv[1]
sums = dict(a.split("=", 1) for a in sys.argv[2:])
d = json.load(open(path))
for c in d["contracts"]:
    c["sha256"] = sums[c["name"]]
with open(path, "w") as f:
    json.dump(d, f, indent=2)
    f.write("\n")
PY
	echo "manifest.json updated"
fi
exit "$failed"
