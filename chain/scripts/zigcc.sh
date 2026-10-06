#!/bin/sh
# C compiler wrapper for cargo builds that cross-compile through zig.
# cc-rs appends --target=<rust triple>, which zig cc cannot parse; the zig target comes from
# ZIG_TARGET instead. Usage: ORAMA_ZIG=<zig> ZIG_TARGET=x86_64-linux-musl zigcc.sh <cc args>
set -eu
: "${ZIG_TARGET:?ZIG_TARGET must name the zig target, for example x86_64-linux-musl}"
zig="${ORAMA_ZIG:-zig}"
for arg in "$@"; do
	shift
	case "$arg" in
	--target=*) ;;
	*) set -- "$@" "$arg" ;;
	esac
done
exec "$zig" cc -target "$ZIG_TARGET" "$@"
