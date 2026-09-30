//go:build e2e_fleet

package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Markers the remote script prints before each section of its output.
const (
	markSigned  = "__E2E_SIGNED__"
	markBcast   = "__E2E_BCAST__"
	markRPCErr  = "__E2E_RPCERR__"
	markResult  = "__E2E_RESULT__"
	markWaitErr = "__E2E_WAITERR__"
	markSignErr = "__E2E_SIGNERR__"
	markEnd     = "__E2E_END__"
	// lockWaitSeconds bounds the wait for a key's turn, inside TxBudget.
	// The queue is FIFO (lockScript), so a waiter is behind at most the
	// transactions that arrived before it, never starved by later ones.
	lockWaitSeconds = 480
	// lockPollSeconds paces a waiter's look at the head of its key's queue.
	lockPollSeconds = "0.2"
	lockDir         = "/run/lock"
)

var lockNamePattern = regexp.MustCompile(`[^a-z0-9-]`)

// lockFile serialises every transaction of one key on its node, across the
// chain packages a stage runs at once: a signer reads its sequence from the
// last committed block, so two in flight would collide.
func lockFile(k Key) string {
	id := k.Name
	if k.KeyringDir != "" {
		id = k.KeyringDir[strings.LastIndex(k.KeyringDir, "/")+1:] + "-" + k.Name
	}
	return lockDir + "/e2e-chain-" + lockNamePattern.ReplaceAllString(id, "-") + ".lock"
}

// lockScript waits for k's turn and takes k's lock, then removes $D and its
// ticket on exit. flock alone is not FIFO: with a stage's chain packages
// queueing on three keys, a waiter could lose the race every time until its
// wait ran out. So each waiter first drops a ticket named by its arrival
// time and pid in the key's queue directory and waits until its ticket is
// the oldest one whose process is still alive (a dead waiter's ticket is
// removed by whoever finds it), then takes the flock.
func lockScript(k Key) string {
	lock := lockFile(k)
	queue := strings.TrimSuffix(lock, ".lock") + ".queue"
	return fmt.Sprintf(`Q=%s
mkdir -p "$Q" || exit 90
TK="$Q/$(date +%%s%%N)-$$"
trap 'rm -f -- "$TK"; rm -rf -- "$D"' EXIT
: > "$TK" || exit 90
END=$(( $(date +%%s) + %d ))
while :; do
  for f in "$Q"/*; do
    [ "$f" = "$TK" ] && break 2
    [ -d "/proc/${f##*-}" ] && break
    rm -f -- "$f"
  done
  [ "$(date +%%s)" -lt "$END" ] || { echo lock timeout >&2; exit 91; }
  sleep %s
done
exec 9>%s || exit 90
flock -w %d 9 || { echo lock timeout >&2; exit 91; }
`, fleet.ShellQuote(queue), lockWaitSeconds, lockPollSeconds, lock, lockWaitSeconds)
}

// feeScript rewrites the fee of $D/u.json: base_fee*gas+delta, or an absolute
// amount. The base fee is read here, under the key's lock, so it is the one
// the next block charges unless that block is over half full (docs/CHAIN.md
// "The base fee (EIP-1559-style)").
func (c *Chain) feeScript(opts TxOptions) string {
	base := OramadCmd("query", "fees", "base-fee", "--node", c.RPC(), "--output", "json")
	abs := opts.FeeAmount
	if abs == "" {
		abs = "0"
	}
	return fmt.Sprintf(`BF=$(%s | python3 -c 'import json,sys;print(json.load(sys.stdin)["base_fee"])') || exit 93
python3 - "$D/u.json" "$BF" %d %d %s %s <<'PY' || exit 94
import json, sys
p, bf, delta, gas, mode, absamt = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), sys.argv[5], int(sys.argv[6])
fee = absamt if mode == "abs" else bf * gas + delta
d = json.load(open(p))
d["auth_info"]["fee"]["amount"] = [{"denom": "norama", "amount": str(fee)}] if fee > 0 else []
json.dump(d, open(p, "w"))
PY
`, base, opts.FeeDelta, opts.gas(), fleet.ShellQuote(string(opts.mode())), fleet.ShellQuote(abs))
}

// signScript signs $D/u.json into $D/s.json with k.
func signScript(c *Chain, k Key, opts TxOptions) string {
	chainID := c.ID
	if opts.ChainID != "" {
		chainID = opts.ChainID
	}
	args := append([]string{"tx", "sign", "$D/u.json", "--from", k.Name, "--chain-id", chainID,
		"--node", c.RPC(), "--output-document", "$D/s.json"}, k.keyringFlags()...)
	if opts.Offline {
		args = append(args, "--offline", "--account-number", fmt.Sprint(opts.AccountNumber),
			"--sequence", fmt.Sprint(opts.Sequence))
	}
	// $D must expand: quote everything, then let the shell see $D.
	cmd := strings.ReplaceAll(OramadCmd(args...), "'$D/", `"$D"'/`)
	return cmd + ` 2> "$D/sign.err" || { echo ` + markSignErr + `; cat "$D/sign.err"; exit 0; }
echo ` + markSigned + `; cat "$D/s.json"; echo
`
}

// broadcastScript broadcasts $D/s.json and waits for its block.
func (c *Chain) broadcastScript() string {
	bcast := strings.ReplaceAll(OramadCmd("tx", "broadcast", "$D/s.json", "--node", c.RPC(), "--output", "json"), "'$D/", `"$D"'/`)
	wait := OramadCmd("query", "wait-tx", "HASH", "--timeout", InclusionTimeout, "--node", c.RPC(), "--output", "json")
	wait = strings.Replace(wait, "'HASH'", `"$H"`, 1)
	return bcast + ` > "$D/b.json" 2> "$D/b.err" || { echo ` + markRPCErr + `; cat "$D/b.err"; exit 0; }
echo ` + markBcast + `; cat "$D/b.json"; echo
C=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("code",0))' "$D/b.json")
[ "$C" = "0" ] || exit 0
H=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["txhash"])' "$D/b.json")
` + wait + ` > "$D/r.json" 2> "$D/r.err" || { echo ` + markWaitErr + `; cat "$D/r.err"; exit 0; }
echo ` + markResult + `; cat "$D/r.json"; echo
`
}

// stageDir creates a private directory on n for one transaction's files and
// uploads name into it. The transaction script removes it (trap); on an
// error here it is removed at once. It needs no test context, so cleanups
// can submit transactions too.
func (c *Chain) stageDir(t testing.TB, n fleet.Node, name string, data []byte) (string, error) {
	t.Helper()
	dir := "/tmp/e2e-chaintx-" + randomHex(t, 8)
	mk, err := c.run(t, n, QueryBudget, fmt.Sprintf("install -d -o %s -g %s -m 0700 %s", ServiceUser, ServiceUser, dir))
	if err != nil || mk.Exit != 0 {
		return "", fmt.Errorf("%s: failed to create %s (exit %d): %v %s", n.Name, dir, mk.Exit, err, mk.Stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), QueryBudget)
	defer cancel()
	if err := c.F.SSHFor(t, n).Put(ctx, dir+"/"+name, data, 0o644); err != nil {
		c.cleanupDir(t, n, dir)
		return "", fmt.Errorf("%s: failed to upload %s: %w", n.Name, name, err)
	}
	return dir, nil
}

// sections splits the script's stdout at its markers.
func sections(stdout string) map[string]string {
	marks := []string{markSigned, markBcast, markRPCErr, markResult, markWaitErr, markSignErr, markSeqBefore, markSeqAfter}
	out := map[string]string{}
	lines := strings.Split(stdout, "\n")
	cur := ""
	for _, l := range lines {
		matched := false
		for _, m := range marks {
			if strings.TrimSpace(l) == m {
				cur, matched = m, true
				break
			}
		}
		if !matched && cur != "" {
			out[cur] += l + "\n"
		}
	}
	return out
}

// parseResult turns the script's sections into a Result.
func parseResult(s map[string]string) (Result, error) {
	r := Result{Signed: []byte(strings.TrimSpace(s[markSigned]))}
	if e, ok := s[markSignErr]; ok {
		return r, fmt.Errorf("oramad tx sign refused the transaction: %s", strings.TrimSpace(e))
	}
	if e, ok := s[markRPCErr]; ok {
		r.Stage, r.Log = StageRPC, strings.TrimSpace(e)
		return r, nil
	}
	var b txResponse
	if err := json.Unmarshal([]byte(s[markBcast]), &b); err != nil {
		return r, fmt.Errorf("failed to decode the broadcast response: %w: %q", err, s[markBcast])
	}
	r.TxHash, r.Code, r.Codespace, r.Log = b.TxHash, b.Code, b.Codespace, b.RawLog
	if b.Code != 0 {
		r.Stage = StageCheck
		return r, nil
	}
	if e, ok := s[markWaitErr]; ok {
		r.Stage, r.Log = StageNotIncluded, strings.TrimSpace(e)
		return r, nil
	}
	var d txResponse
	if err := json.Unmarshal([]byte(s[markResult]), &d); err != nil {
		return r, fmt.Errorf("failed to decode the delivered transaction: %w: %q", err, s[markResult])
	}
	r.Stage, r.Code, r.Codespace, r.Log = StageBlock, d.Code, d.Codespace, d.RawLog
	r.Height, r.GasUsed, r.GasWanted, r.Events, r.Data = d.Height.Int64(), d.GasUsed.Int64(), d.GasWanted.Int64(), d.Events, d.Data
	return r, nil
}
