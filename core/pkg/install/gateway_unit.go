package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// The gateway template (core/systemd/orama-namespace-gateway@.service) lets a
// gateway write its own namespace's directory and read two secrets. The
// cluster gateway, orama-namespace-gateway@index, is the one that does more:
// its namespace cluster manager writes every namespace's directory on the host
// (configs, env inputs) and the shared TURN server's config, and its join
// handler hands a joining node every secret the cluster holds. It gets that in
// a drop-in for its instance only, so no tenant's gateway does.

// indexGatewayDropInDir is the drop-in directory of the cluster gateway's
// instance, in root-owned /etc/systemd/system.
const indexGatewayDropInDir = "/etc/systemd/system/orama-namespace-gateway@index.service.d"

// indexGatewayDropInName is the drop-in's file name.
const indexGatewayDropInName = "10-cluster-gateway.conf"

// IndexGatewayDropIn is the drop-in's content. An empty assignment resets a
// list setting, which is how the template's secrets view is taken back.
const IndexGatewayDropIn = `# Written by the Orama installer on install and upgrade (pkg/install/gateway_unit.go).
[Service]
ReadWritePaths=/opt/orama/.orama/data/namespaces /opt/orama/.orama/data/turn
TemporaryFileSystem=
BindReadOnlyPaths=
ReadOnlyPaths=/opt/orama/.orama/secrets
`

// installIndexGatewayDropIn writes IndexGatewayDropIn. The caller reloads
// systemd.
func installIndexGatewayDropIn() error {
	if err := os.MkdirAll(indexGatewayDropInDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", indexGatewayDropInDir, err)
	}
	path := filepath.Join(indexGatewayDropInDir, indexGatewayDropInName)
	if err := os.WriteFile(path, []byte(IndexGatewayDropIn), 0o644); err != nil {
		return fmt.Errorf("write the cluster gateway's drop-in %s: %w", path, err)
	}
	return nil
}
