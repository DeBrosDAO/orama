#!/usr/bin/env bash
# Runs on a stagenet node, as root (deploy.sh faucet stages and runs it with sudo). Turns the node's
# public faucet on: adds `chain: faucet: enabled: true` to node.yaml. It writes only that block and
# only to a node.yaml with no `chain:` block at all; it does not restart the node (deploy.sh does,
# through the orama CLI). Running it on a node that already has the block is a no-op.
set -euo pipefail

NODE_YAML=/opt/orama/.orama/configs/node.yaml

if [ ! -f "$NODE_YAML" ]; then
	echo "$NODE_YAML does not exist: this node has no cluster install to serve a faucet from" >&2
	exit 1
fi
if grep -qE '^chain:' "$NODE_YAML"; then
	if awk '/^chain:/{c=1;next} /^[^ #]/{c=0} c&&/^ +faucet:/{f=1;next} f&&/^ +enabled: *true/{found=1} END{exit !found}' "$NODE_YAML"; then
		echo "chain.faucet is already enabled in $NODE_YAML"
		exit 0
	fi
	echo "$NODE_YAML has a chain: block without chain.faucet.enabled: true; edit it by hand (website/src/docs/operator/global-nodes.mdx, \"A public faucet\")" >&2
	exit 1
fi
printf '\nchain:\n  faucet:\n    enabled: true\n' >> "$NODE_YAML"
echo "chain.faucet.enabled: true written to $NODE_YAML"
