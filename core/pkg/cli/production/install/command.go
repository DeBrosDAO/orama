package install

import (
	"os"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/invite"
	"github.com/mattn/go-isatty"
)

// Run executes the install command.
//
// Whether the install happens here or over SSH used to be decided by
// os.Geteuid(): running without sudo silently turned "install this machine"
// into "SSH somewhere and install that". The two do very different things to
// very different machines, and nothing said which one was about to happen.
// --remote makes it explicit, and running without root and without --remote is
// refused rather than reinterpreted.
func Run(flags *Flags) error {
	if err := flags.applyInvite(); err != nil {
		return err
	}
	if err := flags.resolveBaseDomain(); err != nil {
		return err
	}

	if flags.Remote {
		remote, err := NewRemoteOrchestrator(flags)
		if err != nil {
			return err
		}
		return remote.Execute()
	}

	if err := clierr.RequireRoot("installing a node on this machine"); err != nil {
		return clierr.Usage("%v\n"+
			"  To install a different machine over SSH, pass --remote", err)
	}

	orchestrator, err := NewOrchestrator(flags)
	if err != nil {
		return err
	}
	return orchestrator.Execute()
}

// resolveBaseDomain fills in the base domain, asking when there is someone to
// ask.
//
// The prompt used to run before the local/remote branch and unconditionally,
// so an install driven from a script or from CI either blocked on a read that
// would never come or took the default and pointed a node at the live devnet.
// Without a terminal the flag is required.
func (f *Flags) resolveBaseDomain() error {
	if f.BaseDomain != "" {
		return nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return clierr.Usage("--base-domain is required when there is no terminal to ask\n" +
			"  e.g. --base-domain orama-devnet.network")
	}
	f.BaseDomain = promptForBaseDomain()
	return nil
}

// applyInvite unpacks an encoded invite into the flags it stands for.
//
// An invite carries the gateway to join and the certificate fingerprint to pin,
// so `--token <invite>` is the whole join. An explicit --join or
// --ca-fingerprint still wins: the operator overriding what the invite says is
// deliberate, and silently ignoring them would be worse than either.
//
// A bare 64-character token decodes to itself and changes nothing, which is
// what a token issued by a cluster that has not been upgraded yet looks like.
func (f *Flags) applyInvite() error {
	if f.Token == "" {
		return nil
	}

	inv, err := invite.Decode(f.Token)
	if err != nil {
		return clierr.Usage("--token is not a valid invite: %w", err)
	}

	f.Token = inv.Token
	if f.JoinAddress == "" {
		f.JoinAddress = inv.JoinURL
	}
	if f.CAFingerprint == "" {
		f.CAFingerprint = inv.CAFingerprint
	}
	return nil
}
