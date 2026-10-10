package setupcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// optionsFromFlags turns the flags and the addresses given as arguments into
// options. It checks nothing about the machines; Options.Normalize does that.
func optionsFromFlags(cmd *cobra.Command, args []string) (setup.Options, error) {
	hostKeys, err := parseHostKeys(flags.hostKeys)
	if err != nil {
		return setup.Options{}, err
	}
	opts := setup.Options{
		Network: flags.network, IPs: append(append([]string(nil), flags.ips...), args...), Name: flags.name,
		ClusterOnly: flags.clusterOnly, Exit: flags.exit, StorageGB: flags.storageGB, Yes: flags.yes,
		User: flags.user, UsePassword: flags.password, BootstrapKey: flags.bootstrapKey, HostKeys: hostKeys,
		Domain: flags.domain, ACMECA: flags.acmeCA, Env: flags.env, Contact: flags.contact,
		ASN: flags.asn, ASNSet: cmd.Flags().Changed("asn"), TorNetwork: flags.torNetwork, NoRelay: flags.noRelay, NoValidator: flags.noValidator,
	}
	return opts, nil
}

// parseHostKeys reads --host-key values: "SHA256:..." for a single machine, or
// "<ip>=SHA256:..." for each of several. The key "" is the single one.
func parseHostKeys(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, v := range values {
		ip, fp, hasIP := strings.Cut(v, "=")
		if !hasIP {
			ip, fp = "", v
		}
		fp = strings.TrimSpace(fp)
		if !strings.HasPrefix(fp, "SHA256:") || len(fp) <= len("SHA256:") {
			return nil, clierr.Usage("--host-key %q: want a fingerprint SHA256:... (as ssh-keygen -l and your provider's console print it), or <ip>=SHA256:...", v)
		}
		if _, dup := out[ip]; dup {
			return nil, clierr.Usage("--host-key names %s twice", orBare(ip))
		}
		out[ip] = fp
	}
	return out, nil
}

func orBare(ip string) string {
	if ip == "" {
		return "the machine"
	}
	return ip
}

// wantWizard says to ask questions: on a terminal, when the addresses are
// missing or a full node has no name, and the person did not ask for no questions.
func wantWizard(opts setup.Options, tty bool) bool {
	if !tty || opts.Yes {
		return false
	}
	return len(opts.IPs) == 0 || (!opts.ClusterOnly && strings.TrimSpace(opts.Name) == "")
}

// requireHostKeys refuses a run that would have to ask about a host key and
// cannot: with --yes, or without a terminal, every machine needs its fingerprint
// on the command line. A host key setup was not given is never trusted.
func requireHostKeys(opts setup.Options, tty bool) error {
	if tty && !opts.Yes {
		return nil
	}
	if err := opts.Normalize(); err != nil {
		return err
	}
	for _, ip := range opts.IPs {
		if opts.HostKeys[ip] == "" && opts.HostKeys[""] == "" {
			return clierr.Usage("no --host-key for %s: unattended, setup trusts only a host key it was given.\n"+
				"  Read the fingerprint in your provider's console (or `ssh-keyscan %s | ssh-keygen -lf -`) and pass --host-key %s",
				ip, ip, hostKeyExample(opts, ip))
		}
	}
	return nil
}

func hostKeyExample(opts setup.Options, ip string) string {
	if len(opts.IPs) == 1 {
		return "SHA256:..."
	}
	return fmt.Sprintf("%s=SHA256:...", ip)
}
