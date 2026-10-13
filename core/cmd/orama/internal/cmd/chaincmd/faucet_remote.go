package chaincmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// runRemote runs a command on a node and returns its stdout. A variable so the tests stand in for
// the node.
var runRemote = remotessh.RunSSHOutput

const (
	markStatus    = "__FAUCET_STATUS__"
	markSigner    = "__FAUCET_SIGNER__"
	markDelivered = "__FAUCET_DELIVERED__"
	markBalance   = "__FAUCET_BALANCE__"
	// inclusionTimeout is how long `oramad query wait-tx` waits for the block.
	inclusionTimeout = "90s"
	// lockWaitSeconds is how long a faucet transaction waits for another one signed with the
	// operator key. `tx sign` reads the key's sequence from the last committed block, so the
	// lock is held until the transaction is in a block: released at broadcast, a second faucet
	// call in the same block signed with the same sequence and was refused. Only faucet calls
	// take this lock; another flow signing with the same key at the same time can still collide.
	lockWaitSeconds = 120
	faucetLockFile  = "/run/lock/orama-chain-faucet.lock"
)

var (
	oramadBin = constants.GlobalBinDir + "/" + constants.ChainDaemonName
	chainRPC  = fmt.Sprintf("tcp://%s:%d", constants.GlobalNetnsAddr, constants.ChainRPCPort)
	statusURL = fmt.Sprintf("http://%s:%d/status", constants.GlobalNetnsAddr, constants.ChainRPCPort)
)

// remoteScript runs script as root on node: bash -c, under sudo for a login that is not root.
func remoteScript(node inspector.Node, script string) (string, error) {
	return runRemote(node, remotessh.SudoPrefix(node)+"bash -c "+clusterops.ShellQuote(script))
}

// oramadFunc defines oramad() in a script: the binary as the chain user, inside the namespace the
// chain listens in (its RPC is on the namespace address, which only that namespace and the host's
// veth end reach), with the chain home.
func oramadFunc() string {
	return fmt.Sprintf("oramad() { ip netns exec %s runuser -u %s -- %s \"$@\" --home %s; }\n",
		globalnetns.Name, constants.ChainUser, oramadBin, constants.ChainHome)
}

// probeScript prints the chain's /status and the operator key's address.
func probeScript() string {
	return fmt.Sprintf(`set -eu
echo %s
curl -fsS --max-time 10 %s
echo
echo %s
%soramad keys show %s -a --keyring-backend test
`, markStatus, statusURL, markSigner, oramadFunc(), faucetOperatorKey)
}

// nodeProbe is what the node says about its chain and its operator key.
type nodeProbe struct {
	ChainID string
	Signer  string
}

// parseProbe reads probeScript's output.
func parseProbe(out string) (nodeProbe, error) {
	sec := splitMarked(out, markStatus, markSigner)
	var st struct {
		Result struct {
			NodeInfo struct {
				Network string `json:"network"`
			} `json:"node_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(sec[markStatus]), &st); err != nil || st.Result.NodeInfo.Network == "" {
		return nodeProbe{}, fmt.Errorf("the node's chain status has no chain id: %q", strings.TrimSpace(sec[markStatus]))
	}
	signer, err := clusterreg.CanonicalAccount(strings.TrimSpace(sec[markSigner]))
	if err != nil {
		return nodeProbe{}, fmt.Errorf("key %s in the node's keyring is not an orama account: %w", faucetOperatorKey, err)
	}
	return nodeProbe{ChainID: st.Result.NodeInfo.Network, Signer: signer}, nil
}

// splitMarked splits out at the marker lines: marker -> the text after it.
func splitMarked(out string, marks ...string) map[string]string {
	sec := map[string]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		matched := false
		for _, m := range marks {
			if strings.TrimSpace(line) == m {
				cur, matched = m, true
				break
			}
		}
		if !matched && cur != "" {
			sec[cur] += line + "\n"
		}
	}
	return sec
}

// faucetTxScript signs unsigned (base64 proto-JSON) with the operator key and broadcasts it,
// printing the broadcast response, and keeps the key's lock until the transaction's block. The fee is gas x the base fee read under the key's lock, which
// is what the next block charges (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md, "The base fee"). The transaction files live in a
// 0700 directory of the chain user, removed on exit; the key never leaves the keyring.
func faucetTxScript(chainID string, unsigned []byte) string {
	return fmt.Sprintf(`set -eu
%sD=$(mktemp -d /tmp/orama-faucet.XXXXXXXXXX)
trap 'rm -rf -- "$D"' EXIT
chown %[2]s: "$D"
chmod 0700 "$D"
exec 9>%[3]s
flock -w %[4]d 9 || { echo "another faucet transaction holds the operator key's lock" >&2; exit 1; }
printf %%s %[5]s | base64 -d > "$D/u.json"
chown %[2]s: "$D/u.json"
BF=$(oramad query fees base-fee --node %[6]s --output json | python3 -c 'import json,sys;print(json.load(sys.stdin)["base_fee"])')
python3 - "$D/u.json" "$BF" %[7]d <<'PY'
import json, sys
p, bf, gas = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
d = json.load(open(p))
d["auth_info"]["fee"]["amount"] = [{"denom": %[8]q, "amount": str(bf * gas)}] if bf * gas > 0 else []
json.dump(d, open(p, "w"))
PY
oramad tx sign "$D/u.json" --from %[9]s --keyring-backend test --chain-id %[10]s --node %[6]s --output-document "$D/s.json" >/dev/null
oramad tx broadcast "$D/s.json" --node %[6]s --output json > "$D/b.json"
cat "$D/b.json"
H=$(python3 -c 'import json,sys;r=json.load(open(sys.argv[1]));print(r.get("txhash","") if r.get("code",0)==0 else "")' "$D/b.json")
# Holds the key's lock until the block: the next faucet call reads its sequence from it. Whether
# the transaction was delivered is judged by resultScript, which waits for the same hash.
[ -z "$H" ] || oramad query wait-tx "$H" --timeout %[11]s --node %[6]s >/dev/null 2>&1 || true
`, oramadFunc(), constants.ChainUser, faucetLockFile, lockWaitSeconds,
		clusterops.ShellQuote(base64.StdEncoding.EncodeToString(unsigned)), chainRPC, faucetGas,
		constants.ChainDenom, faucetOperatorKey, clusterops.ShellQuote(chainID), inclusionTimeout)
}

// txResponse is the part of cosmos.base.abci.v1beta1.TxResponse the report needs.
type txResponse struct {
	Height    string `json:"height"`
	TxHash    string `json:"txhash"`
	Codespace string `json:"codespace"`
	Code      uint32 `json:"code"`
	RawLog    string `json:"raw_log"`
}

// parseBroadcast reads the broadcast response and turns a CheckTx refusal into an error.
func parseBroadcast(out string) (txResponse, error) {
	var r txResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &r); err != nil {
		return r, fmt.Errorf("the broadcast answer is not a transaction response: %w: %q", err, strings.TrimSpace(out))
	}
	if r.Code != 0 {
		return r, refused("the chain refused the faucet transaction before a block", r)
	}
	if r.TxHash == "" {
		return r, fmt.Errorf("the broadcast answer has no transaction hash: %q", strings.TrimSpace(out))
	}
	return r, nil
}

// refused is the chain's refusal, in its own words.
func refused(what string, r txResponse) error {
	return clierr.Failure("%s (code %d, codespace %q): %s", what, r.Code, r.Codespace, r.RawLog)
}

// resultScript waits for hash's block and prints the delivered transaction, then the recipient's
// bank balance.
func resultScript(hash, recipient string) string {
	return fmt.Sprintf(`set -eu
%secho %s
oramad query wait-tx %s --timeout %s --node %s --output json
echo
echo %s
oramad query bank balance %s %s --node %s --output json
`, oramadFunc(), markDelivered, clusterops.ShellQuote(hash), inclusionTimeout, chainRPC,
		markBalance, clusterops.ShellQuote(recipient), constants.ChainDenom, chainRPC)
}

// parseResult reads resultScript's output: the delivered transaction (a refusal in the block is an
// error) and the recipient's balance of norama.
func parseResult(out string) (txResponse, *big.Int, error) {
	sec := splitMarked(out, markDelivered, markBalance)
	var r txResponse
	if err := json.Unmarshal([]byte(sec[markDelivered]), &r); err != nil {
		return r, nil, fmt.Errorf("the delivered transaction is not a transaction response: %w: %q", err, strings.TrimSpace(sec[markDelivered]))
	}
	if r.Code != 0 {
		return r, nil, refused("the chain refused the faucet transaction in its block", r)
	}
	var b struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
	}
	if err := json.Unmarshal([]byte(sec[markBalance]), &b); err != nil {
		return r, nil, fmt.Errorf("the balance answer is not a bank balance: %w: %q", err, strings.TrimSpace(sec[markBalance]))
	}
	bal, ok := new(big.Int).SetString(b.Balance.Amount, 10)
	if !ok {
		return r, nil, fmt.Errorf("the balance %q is not a number", b.Balance.Amount)
	}
	return r, bal, nil
}
