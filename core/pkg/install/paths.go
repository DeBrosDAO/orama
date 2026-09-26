package install

import (
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// Central path constants for the Orama Network production environment.
// All services run as root with /opt/orama as the base directory.
const (
	OramaBase    = "/opt/orama"
	OramaBinDir  = "/opt/orama/bin"
	OramaDir     = "/opt/orama/.orama"
	OramaConfigs = "/opt/orama/.orama/configs"
	OramaSecrets = "/opt/orama/.orama/secrets"
	OramaData    = "/opt/orama/.orama/data"
	OramaLogs    = "/opt/orama/.orama/logs"

	// Pre-built binary archive paths (created by `orama build`)
	OramaManifest   = "/opt/orama/manifest.json"
	OramaArchiveBin = "/opt/orama/bin"     // Pre-built binaries
	OramaSystemdDir = "/opt/orama/systemd" // Namespace service templates
)

// WireGuardInterface is the overlay interface every node's inter-node traffic
// rides on. All public IPs are for SSH and external HTTPS only, so if this
// interface is down the node is partitioned from the cluster.
const WireGuardInterface = "wg0"

// OramaRoot anchors rootfs at the directory holding oramaDir — OramaBase on a
// node — which only root may write. Install and upgrade run as root and reach
// the orama user's .orama tree through it, so a symlink the orama user plants
// there is refused rather than followed (docs/SECURITY.md).
func OramaRoot(oramaDir string) rootfs.Root {
	return rootfs.At(filepath.Dir(oramaDir))
}
