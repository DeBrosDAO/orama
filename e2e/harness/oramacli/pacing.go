package oramacli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

// Pacing the CLI (e2e/README.md, "Pacing"). The CLI calls the gateway's
// credential routes itself, so the runner spends the run's pacer tokens for
// it, against GatewayHost:
//
//   - `auth login` with a wallet: challenge + verify: the Wallet's challenge
//     bucket once, the address bucket twice, before the command runs.
//   - `auth login` on a NoWallet runner: /v1/auth/device once before; every
//     /v1/auth/device/token poll is charged as the CLI prints its progress dot.
//   - `auth approve`: challenge + device/approve, like a wallet login.
//   - any other command may renew its session (refresh, or an API-key
//     exchange): when the HOME's credentials file changed, one address token
//     is charged afterwards.

// CredentialsFile is where the CLI stores its sessions under HOME/.orama.
const CredentialsFile = "credentials.json"

// Token costs of the commands whose credential calls are known beforehand.
const (
	walletLoginCred = 2 // /v1/auth/challenge + /v1/auth/verify
	approveCred     = 2 // /v1/auth/challenge + /v1/auth/device/approve
	deviceLoginCred = 1 // /v1/auth/device; the polls are charged as they happen
)

// pollMarker ends the line after which the device login prints one dot per
// poll of /v1/auth/device/token (core/cmd/orama/internal/auth_commands.go).
const pollMarker = "Waiting"

// cost is what one command spends.
type cost struct {
	cred      int
	challenge bool
	device    bool
	// known: the cost is charged beforehand, so no renewal is looked for.
	known bool
}

// commandCost classifies args: "auth login" / "auth approve" anywhere in the
// arguments (flags may come before or between).
func commandCost(args []string, noWallet bool) cost {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "auth" {
			continue
		}
		switch args[i+1] {
		case "login":
			if noWallet {
				return cost{cred: deviceLoginCred, device: true, known: true}
			}
			return cost{cred: walletLoginCred, challenge: true, known: true}
		case "approve":
			if noWallet {
				// No agent to sign with: the command stops at its own argument
				// or agent check, before any challenge is asked for.
				return cost{known: true}
			}
			return cost{cred: approveCred, challenge: true, known: true}
		}
	}
	return cost{}
}

// pacePlan is one invocation's pacing: what was spent before it ran and
// what to charge while and after it runs. A nil plan paces nothing.
type pacePlan struct {
	pacer       *pace.Pacer
	host        string
	cost        cost
	credsPath   string
	credsBefore [sha256.Size]byte
	polls       pollCounter
}

func (r *Runner) resolvePacer() (*pace.Pacer, error) {
	if r.Pacer != nil {
		return r.Pacer, nil
	}
	p, err := pace.FromEnv(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to set up the CLI's credential pacing: %w", err)
	}
	return p, nil
}

// exchangesEnvToken reports whether env (KEY=VALUE, the last one winning, as
// exec does) hands the CLI an ORAMA_TOKEN it will exchange: anything that is
// not already a token. The CLI decides that with auth.LooksLikeJWT, which is
// used here so the two cannot drift. Given an API key, the CLI exchanges it on
// /v1/auth/token before its command runs: one credential request that changes
// no file, so nothing after the run would see it.
func exchangesEnvToken(env []string) bool {
	value, set := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == auth.TokenEnvVar {
			value, set = strings.TrimSpace(v), true
		}
	}
	if !set || value == "" {
		return false
	}
	return !auth.LooksLikeJWT(value)
}

// paceBefore waits for the tokens args spends beforehand. extraEnv is the
// invocation's own additions to the runner's Env.
func (r *Runner) paceBefore(ctx context.Context, args, extraEnv []string) (*pacePlan, error) {
	p, err := r.resolvePacer()
	if err != nil || p == nil {
		return nil, err
	}
	cmdline := strings.Join(RedactArgs(args), " ")
	if r.GatewayHost == "" {
		return nil, fmt.Errorf("refusing to run orama %s: the runner has no GatewayHost to pace its credential calls against (build it with ForState)", cmdline)
	}
	plan := &pacePlan{pacer: p, host: r.GatewayHost, cost: commandCost(args, r.noWallet),
		credsPath: filepath.Join(r.Home, ConfigDirName, CredentialsFile)}
	if exchangesEnvToken(append(append([]string{}, r.Env...), extraEnv...)) {
		plan.cost.cred++
	}
	if plan.cost.challenge {
		if r.Wallet == "" {
			return nil, fmt.Errorf("refusing to run orama %s: it signs a challenge, and the runner has no Wallet to pace the per-wallet challenge bucket (set Runner.Wallet to the address its agent signs with)", cmdline)
		}
		if err := p.Wait(ctx, r.GatewayHost, pace.ChallengeBucket(r.Wallet)); err != nil {
			return nil, fmt.Errorf("orama %s: %w", cmdline, err)
		}
	}
	for i := 0; i < plan.cost.cred; i++ {
		if err := p.Wait(ctx, r.GatewayHost, pace.BucketCred); err != nil {
			return nil, fmt.Errorf("orama %s: %w", cmdline, err)
		}
	}
	if !plan.cost.known {
		if plan.credsBefore, err = fileDigest(plan.credsPath); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// observe charges the device-login polls a chunk of stdout reports.
func (pl *pacePlan) observe(chunk []byte) error {
	if pl == nil || !pl.cost.device {
		return nil
	}
	for n := pl.polls.feed(chunk); n > 0; n-- {
		if err := pl.pacer.Charge(pl.host, pace.BucketCred); err != nil {
			return fmt.Errorf("failed to charge a device-login poll: %w", err)
		}
	}
	return nil
}

// after charges a session renewal: the credentials file changed during a
// command whose cost was not known beforehand.
func (pl *pacePlan) after() error {
	if pl == nil || pl.cost.known {
		return nil
	}
	d, err := fileDigest(pl.credsPath)
	if err != nil || d == pl.credsBefore {
		return err
	}
	if err := pl.pacer.Charge(pl.host, pace.BucketCred); err != nil {
		return fmt.Errorf("failed to charge a session renewal: %w", err)
	}
	return nil
}

// fileDigest is the SHA-256 of path, zero when it does not exist.
func fileDigest(path string) ([sha256.Size]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return [sha256.Size]byte{}, nil
	}
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("failed to read the CLI's credentials %s: %w", path, err)
	}
	return sha256.Sum256(raw), nil
}

// pollCounter counts the progress dots the device login prints after
// pollMarker, up to the end of that line, across chunk boundaries.
type pollCounter struct {
	seen, done bool
	carry      string
}

func (c *pollCounter) feed(chunk []byte) int {
	if c.done {
		return 0
	}
	text := c.carry + string(chunk)
	if !c.seen {
		i := strings.Index(text, pollMarker)
		if i < 0 {
			c.carry = text[max(0, len(text)-len(pollMarker)+1):]
			return 0
		}
		c.seen, c.carry = true, ""
		text = text[i+len(pollMarker):]
	}
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		text, c.done = text[:nl], true
	}
	return strings.Count(text, ".")
}
