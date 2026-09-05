package production

// Central path constants for the Orama Network production environment.
// All services run as root with /opt/orama as the base directory.
const (
	OramaBase    = "/opt/orama"
	OramaBinDir  = "/opt/orama/bin"
	OramaSrcDir  = "/opt/orama/src"
	OramaDir     = "/opt/orama/.orama"
	OramaConfigs = "/opt/orama/.orama/configs"
	OramaSecrets = "/opt/orama/.orama/secrets"
	OramaData    = "/opt/orama/.orama/data"
	OramaLogs    = "/opt/orama/.orama/logs"

	// Pre-built binary archive paths (created by `orama build`)
	OramaManifest    = "/opt/orama/manifest.json"
	OramaManifestSig = "/opt/orama/manifest.sig"
	OramaArchiveBin  = "/opt/orama/bin"      // Pre-built binaries
	OramaSystemdDir  = "/opt/orama/systemd"  // Namespace service templates
	OramaPackagesDir = "/opt/orama/packages" // .deb packages (e.g., anon.deb)
)

// WireGuardInterface is the overlay interface every node's inter-node traffic
// rides on. All public IPs are for SSH and external HTTPS only, so if this
// interface is down the node is partitioned from the cluster.
const WireGuardInterface = "wg0"
