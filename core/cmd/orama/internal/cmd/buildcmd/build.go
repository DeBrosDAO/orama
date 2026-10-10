package buildcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/spf13/cobra"
)

var buildFlags build.Flags

// Cmd is the top-level build command.
var Cmd = &cobra.Command{
	Use:   "build",
	Short: "Build pre-compiled binary archive for deployment",
	Long: `Cross-compile all Orama binaries and dependencies for Linux,
then package them into a deployment archive. The archive includes:
  - Orama binaries (CLI, node, gateway, identity, SFU, TURN)
  - Olric, IPFS Kubo, IPFS Cluster, RQLite, CoreDNS, Caddy (built from
    checked-in, checksum-pinned modules; Kubo and RQLite by pinned digest)
  - The global layer, for amd64: oramad (with the orchard verifier linked) and its
    out-of-process verifier orama-orchard-verifier, orama-global and the pinned
    cosmovisor release, which 'orama global install' puts on a node. It needs
    rustup with the x86_64-unknown-linux-musl target, cargo, make and rsync.
    --skip-global-layer leaves it out (a cluster-only archive; arm64 always is).
  - Systemd namespace templates
  - manifest.json with checksums of every file, and manifest.sig

The manifest is signed with your RootWallet (the agent's active account, through
its wallet:sign capability). Nodes install only archives signed by an address in
their trust anchor, /etc/orama/archive-signers, so signing is the default;
--unsigned makes an archive without a wallet signature: the CI build that release
signers sign. A node installs it only when its release root accepted it
('orama maint node stage-archive --release-only'); otherwise it is for local inspection.

--signers rotates the trusted signers: nodes that install this build replace
their list with the given addresses. The build must be signed by a signer the
nodes trust now, and the list must include that signer; retiring a key takes
two builds (the old key adds the new one, the new key then drops the old).

--release-root <root.json> puts a TUF release root in the signed manifest. A node that
installs the build adopts it (/etc/orama/release-root.json) the way it takes a signer
rotation, and from then on accepts releases signed under that root
('orama maint node stage-archive --release-only', the auto-update agent).

The build is reproducible: with SOURCE_DATE_EPOCH set (a release build sets it to
the commit's time) two builds of one commit produce the same archive, byte for
byte. See docs/DEV_DEPLOY.md, "Reproducible builds".

The resulting archive can be pushed to nodes with 'orama maint push'.

Examples:
  orama maint build
  orama maint build --signers 0xYourWallet,0xNewOperator
  orama maint build --unsigned --output /tmp/inspect.tar.gz`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return build.Run(&buildFlags)
	},
}

func init() {
	f := Cmd.Flags()
	f.StringVar(&buildFlags.Arch, "arch", "amd64", "Target architecture (amd64, arm64)")
	f.StringVar(&buildFlags.Output, "output", "", "Output archive path (default: /tmp/orama-<version>-linux-<arch>.tar.gz)")
	f.BoolVar(&buildFlags.SkipGlobalLayer, "skip-global-layer", false,
		"Leave out the global layer (oramad, its verifier, orama-global, cosmovisor): a cluster-only archive")
	f.BoolVar(&buildFlags.Verbose, "verbose", false, "Verbose output")
	f.BoolVar(&buildFlags.Unsigned, "unsigned", false, "Do not sign the manifest (a node installs it only through its adopted TUF release root)")
	f.StringSliceVar(&buildFlags.Signers, "signers", nil,
		"Rotate the trusted archive signers: nodes that install this build trust only these addresses (comma-separated)")

	f.StringVar(&buildFlags.ReleaseRoot, "release-root", "",
		"A TUF root.json to put in the signed manifest: nodes that install this build adopt it as their release root")
}
