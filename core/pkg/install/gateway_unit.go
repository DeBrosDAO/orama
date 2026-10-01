package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gatewaykeys"
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
# The index gateway's signing keys. The files are root:root 0400; this unit
# is the only one that receives them. LoadCredential= has no optional form (a
# "-" prefix is rejected and the line ignored), so the unit does not start
# unless both files exist: the installer creates them first (ensureIndexGatewayKeys).
LoadCredential=jwt-signing-key:/var/lib/orama-gateway-keys/index/jwt-signing-key.pem
LoadCredential=jwt-eddsa-key:/var/lib/orama-gateway-keys/index/jwt-eddsa-key.pem
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

// ensureIndexGatewayKeys creates the index gateway's signing keys when they are
// missing, before the unit that loads them is (re)started. A key already
// stored is left alone; a missing one is taken from the state directory an
// earlier release kept it in, and generated only when there is none there.
func ensureIndexGatewayKeys(oramaDir string) ([]string, error) {
	stateDir := constants.GatewayStateDir(constants.NamespacesDir(oramaDir), constants.IndexNamespace)
	root := OramaRoot(oramaDir)
	readLegacy := func(name string) ([]byte, error) {
		keyPEM, err := root.ReadFile(filepath.Join(stateDir, name), gatewaykeys.MaxPEM)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return keyPEM, err
	}
	created, err := gatewaykeys.Ensure(gatewaykeys.Dir, readLegacy)
	if err != nil {
		return nil, fmt.Errorf("make sure the index gateway's signing keys exist in %s: %w", gatewaykeys.Dir, err)
	}
	return created, nil
}
