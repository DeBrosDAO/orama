package globalcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

const (
	// passphraseFileMax bounds the passphrase file.
	passphraseFileMax = 4096
	// secretFileMask is every permission bit a secret file must not have.
	secretFileMask = 0o077

	defaultVotingIntervalMinutes = 60
	defaultVoteDelaySeconds      = 300
	defaultDistDelaySeconds      = 300
)

var torCmd = &cobra.Command{
	Use:   "tor",
	Short: "The Orama Tor network: authority key ceremony, node identity, vote archive",
	Long: `The Orama Tor network is a separate anonymity network built from unmodified
upstream Tor code, run by Orama's own directory authorities (docs/TOR_NETWORK.md).
The roles are installed by 'orama global install --services dirauth|relay|relay,exit|onion'.`,
}

var ceremonyFlags struct {
	name          string
	out           string
	authorities   []string
	interval      int
	voteDelay     int
	distDelay     int
	allowExit     bool
	sharedSubnets bool
	hsdirHours    int
	bootstrap     bool
	passphrase    string
	torBinary     string
	gencertBinary string
}

var ceremonyCmd = &cobra.Command{
	Use:   "ceremony",
	Short: "Generate the directory authorities' keys and the network file (run on an offline machine)",
	Long: `Generate the keys of a set of directory authorities with the upstream tor and
tor-gencert, which must be installed on this machine, and write the network file.

Run it on an air-gapped machine. For each --authority NICKNAME=IPv4 it writes,
below --out:
  offline/<nickname>/authority_identity_key   the identity key, encrypted with the
                                              passphrase; it signs certificates and
                                              nothing else. Move it to offline media
                                              (encrypted, in two places, or an HSM)
                                              and delete it from this machine.
  deploy/<nickname>/keys/                     what the authority host installs: the
                                              signing key and its 12-month
                                              certificate, and the relay identity.
and once tor-network.json (public: the authority list every relay and client
needs, to stage beside the release) and TRANSCRIPT.txt (the fingerprints to read
aloud and sign). --out must not exist or be empty: a ceremony never writes over
keys.

--passphrase-file holds the identity-key passphrase (at least 16 characters, one
line, mode 0600). Authority ports are fixed at ` + fmt.Sprint(constants.GlobalTorORPort) + ` (ORPort) and ` + fmt.Sprint(constants.GlobalTorDirPort) + ` (DirPort),
the ports the global firewall opens. --bootstrap (default) writes the network file
with bootstrap true, which a new network needs for its first consensus; set it to
false in the file once the first consensus is signed. --allow-exit puts allow_exit
in the file: only a network whose owner runs exits sets it.`,
	Args: cobra.NoArgs,
	RunE: runCeremony,
}

func runCeremony(cmd *cobra.Command, _ []string) error {
	f := ceremonyFlags
	specs, err := parseAuthoritySpecs(f.authorities)
	if err != nil {
		return err
	}
	if f.name == "" || f.out == "" || f.passphrase == "" {
		return clierr.Usage("--name, --out, --authority (three or more) and --passphrase-file are required")
	}
	phrase, err := readPassphrase(f.passphrase)
	if err != nil {
		return err
	}
	res, err := tornet.RunCeremony(cmd.Context(), runProgram, tornet.CeremonyRequest{
		Network: tornet.Network{
			Name: f.name, Private: true, Bootstrap: f.bootstrap, AllowExit: f.allowExit,
			AllowSharedSubnets: f.sharedSubnets, HSDirMinUptimeHours: f.hsdirHours,
			VotingIntervalMinutes: f.interval, VoteDelaySeconds: f.voteDelay, DistDelaySeconds: f.distDelay,
		},
		Specs: specs, OutDir: f.out, Passphrase: phrase,
		TorBinary: f.torBinary, GencertBinary: f.gencertBinary, Now: time.Now(),
	})
	if err != nil {
		return clierr.Failure("%v", err)
	}
	out := cmd.OutOrStdout()
	for _, a := range res.Network.Authorities {
		fmt.Fprintf(out, "%s %s fingerprint %s v3ident %s (certificate expires %s)\n",
			a.Nickname, a.Address, a.Fingerprint, a.V3Ident, res.Expires[a.Nickname].Format(time.DateOnly))
	}
	fmt.Fprintf(out, "wrote %s/%s and %s/%s\nMove %s/%s to offline media and delete it from this machine.\n",
		f.out, constants.TorNetworkFile, f.out, tornet.CeremonyTranscript, f.out, tornet.CeremonyOfflineDir)
	return nil
}

// parseAuthoritySpecs reads NICKNAME=IPv4 pairs.
func parseAuthoritySpecs(pairs []string) ([]tornet.AuthoritySpec, error) {
	var specs []tornet.AuthoritySpec
	for _, p := range pairs {
		nick, addr, ok := strings.Cut(p, "=")
		if !ok || nick == "" || addr == "" {
			return nil, clierr.Usage("--authority %q must be NICKNAME=IPv4", p)
		}
		specs = append(specs, tornet.AuthoritySpec{Nickname: nick, Address: addr, ORPort: constants.GlobalTorORPort, DirPort: constants.GlobalTorDirPort})
	}
	return specs, nil
}

// readPassphrase reads the passphrase file: a regular file only its owner can
// read, one line.
func readPassphrase(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, clierr.Usage("--passphrase-file: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&secretFileMask != 0 || info.Size() > passphraseFileMax {
		return nil, clierr.Usage("--passphrase-file %s must be a regular file (not a link) of mode 0600 (or stricter) under %d bytes", path, passphraseFileMax)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return nil, clierr.Usage("--passphrase-file %s belongs to another account", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, clierr.Failure("read --passphrase-file: %v", err)
	}
	return bytes.TrimRight(raw, "\r\n"), nil
}

// runProgram is the ceremony's Runner: it runs the program with stdin.
func runProgram(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	return c.CombinedOutput()
}

var archiveFlags struct{ dataDir, archiveDir, bandwidthFile, exportVotesDir string }

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Archive this directory authority's consensus and votes (run by orama-global-tor-archive.timer)",
	Long: `Copy the consensus the authority holds, the votes that made it and, with
--bandwidth-file, the bandwidth file it voted with, from --data-dir into
--archive-dir/<valid-after>/ with a MANIFEST.json of SHA-256 digests. Every vote,
consensus and bandwidth file of the network is then recomputable by anyone who
holds the archive. Running it again changes nothing for a period that is
archived; a consensus is replaced only by the same consensus with more
signatures.
Before the authority's first consensus (up to one voting interval after the
authorities start) there is nothing to archive, and the run succeeds saying so.

With --export-votes-dir it also copies the authority's own vote of that period
to <dir>/<valid-after>.vote, the files the bandwidth reporter reads. The
directory must exist (the install makes it, shared read-only with the
reporter's group); nothing else of the data directory is copied there.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := archiveFlags
		if f.dataDir == "" || f.archiveDir == "" {
			return clierr.Usage("--data-dir and --archive-dir are required")
		}
		m, wrote, err := tornet.ArchiveVotingPeriod(f.dataDir, f.archiveDir, f.bandwidthFile)
		if errors.Is(err, tornet.ErrNoConsensusYet) {
			fmt.Fprintf(cmd.OutOrStdout(), "nothing to archive: %v\n", err)
			return nil
		}
		if err != nil {
			return clierr.Failure("%v", err)
		}
		state, votes := "already archived", ""
		if wrote {
			state = "archived"
		}
		if m.VotesMissing {
			votes = " (the votes of this period are not held)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: valid-after %s root %s%s\n", state, m.ValidAfter, m.Root, votes)
		if f.exportVotesDir == "" {
			return nil
		}
		exported, err := tornet.ExportOwnVote(f.dataDir, f.exportVotesDir)
		if err != nil {
			return clierr.Failure("export the authority's own vote: %v", err)
		}
		if exported {
			fmt.Fprintf(cmd.OutOrStdout(), "exported the authority's own vote to %s\n", f.exportVotesDir)
		}
		return nil
	},
}

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show this node's Tor identities and the consensus it holds (run as root)",
	Long: `For each Tor role installed on this node (directory authority, relay or exit,
validator onion service), print the nickname and fingerprints tor made (what
'MsgRegisterRelay' and a node's onion endpoint need), the onion address, and a
summary of the consensus the process holds: when it is valid, how many relays
it lists, and whether it lists this relay. A role that has not started yet shows
no identity. The root's --json prints the same as a JSON array.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := clierr.RequireRoot("reading the Tor data directories"); err != nil {
			return err
		}
		var infos []tornet.NodeInfo
		for _, home := range []string{constants.GlobalTorDirauthHome, constants.GlobalTorRelayHome, constants.GlobalTorOnionHome} {
			if _, err := os.Stat(home); os.IsNotExist(err) {
				continue
			}
			info, err := tornet.ReadNodeInfo(home, time.Now())
			if err != nil {
				info = tornet.NodeInfo{Home: home, Error: err.Error()}
			}
			infos = append(infos, info)
		}
		if len(infos) == 0 {
			return clierr.NotFound("no Tor role is installed on this node: orama global install --services dirauth|relay|onion")
		}
		if err := printTorInfo(cmd, infos, printer.For(cmd).JSONMode()); err != nil {
			return clierr.Failure("%v", err)
		}
		for _, i := range infos {
			if i.Error != "" {
				return clierr.Failure("%s could not be read: %s", i.Home, i.Error)
			}
		}
		return nil
	},
}

func printTorInfo(cmd *cobra.Command, infos []tornet.NodeInfo, asJSON bool) error {
	out := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(infos)
	}
	for _, i := range infos {
		fmt.Fprintf(out, "%s\n", i.Home)
		if i.Error != "" {
			fmt.Fprintf(out, "  error        %s\n", i.Error)
			continue
		}
		for _, kv := range [][2]string{{"nickname", i.Nickname}, {"fingerprint", i.Fingerprint}, {"ed25519", i.Ed25519ID}, {"onion", i.Onion}} {
			if kv[1] != "" {
				fmt.Fprintf(out, "  %-12s %s\n", kv[0], kv[1])
			}
		}
		if c := i.Consensus; c != nil {
			fmt.Fprintf(out, "  consensus    %s, valid after %s, fresh until %s (fresh %t, valid %t)\n", c.Flavor, c.ValidAfter.Format(time.RFC3339), c.FreshUntil.Format(time.RFC3339), c.Fresh, c.Valid)
			fmt.Fprintf(out, "  network      %d relays, %d running, %d exits, %d guards, %d signatures\n", c.Relays, c.Running, c.Exits, c.Guards, c.Signatures)
			if i.Fingerprint != "" {
				fmt.Fprintf(out, "  listed       %t %s\n", c.Listed, strings.Join(c.ListedFlags, " "))
			}
		} else {
			fmt.Fprintf(out, "  consensus    none yet\n")
		}
	}
	return nil
}

func init() {
	c := ceremonyCmd.Flags()
	c.StringVar(&ceremonyFlags.name, "name", "", "Network name, lowercase letters, digits and dashes [required]")
	c.StringVar(&ceremonyFlags.out, "out", "", "Output directory; must not exist or be empty [required]")
	c.StringArrayVar(&ceremonyFlags.authorities, "authority", nil, "A directory authority, NICKNAME=IPv4 (repeatable, at least three) [required]")
	c.IntVar(&ceremonyFlags.interval, "voting-interval-minutes", defaultVotingIntervalMinutes, "Minutes between consensuses; must divide 24 hours")
	c.IntVar(&ceremonyFlags.voteDelay, "vote-delay-seconds", defaultVoteDelaySeconds, "Seconds authorities wait for votes")
	c.IntVar(&ceremonyFlags.distDelay, "dist-delay-seconds", defaultDistDelaySeconds, "Seconds authorities wait for signatures")
	c.BoolVar(&ceremonyFlags.allowExit, "allow-exit", false, "Let nodes of this network be installed as exits")
	c.BoolVar(&ceremonyFlags.sharedSubnets, "allow-shared-subnets", false, "Let circuits use two relays of one /16 (a network with fewer /16 networks than hops needs it)")
	c.IntVar(&ceremonyFlags.hsdirHours, "hsdir-min-uptime-hours", 0, "Hours of uptime before a relay gets the HSDir flag (0 = Tor's default of 96; a new network sets a few)")
	c.BoolVar(&ceremonyFlags.bootstrap, "bootstrap", true, "Write bootstrap true: a new network assumes reachability until its first consensus")
	c.StringVar(&ceremonyFlags.passphrase, "passphrase-file", "", "File holding the identity-key passphrase, mode 0600 [required]")
	c.StringVar(&ceremonyFlags.torBinary, "tor", "", "The tor binary (default: tor on PATH)")
	c.StringVar(&ceremonyFlags.gencertBinary, "tor-gencert", "", "The tor-gencert binary (default: tor-gencert on PATH)")
	a := archiveCmd.Flags()
	a.StringVar(&archiveFlags.dataDir, "data-dir", "", "The authority's tor DataDirectory [required]")
	a.StringVar(&archiveFlags.archiveDir, "archive-dir", "", "Where the archive is written [required]")
	a.StringVar(&archiveFlags.bandwidthFile, "bandwidth-file", "", "The bandwidth file the authority votes with")
	a.StringVar(&archiveFlags.exportVotesDir, "export-votes-dir", "", "Also copy the authority's own vote to <dir>/<valid-after>.vote for the bandwidth reporter (the directory must exist)")
	torCmd.AddCommand(ceremonyCmd, cmdmeta.MarkNodeLocal(archiveCmd), infoCmd)
	Cmd.AddCommand(torCmd)
}
