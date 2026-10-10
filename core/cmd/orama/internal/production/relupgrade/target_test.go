package relupgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// registryWith is a registry holding one valid network called name.
func registryWith(t *testing.T, name string) *netregistry.Registry {
	t.Helper()
	root := []byte(`{"signed":{"_type":"root"}}`)
	sum := sha256.Sum256(root)
	manifest := fmt.Sprintf(`{"name":%q,"chain_id":"orama-%s-6","genesis_sha256":%q,"seeds":["seed1.%s.orama.network"],`+
		`"channel":"nightly","min_version":"0.3.0","release_repo":"https://releases.orama.network",`+
		`"release_root_sha256":%q,"faucet":true}`, name, name, strings.Repeat("b", 64), name, hex.EncodeToString(sum[:]))
	reg, err := netregistry.LoadFS(fstest.MapFS{
		"r/" + name + "/" + netregistry.ManifestFile:    {Data: []byte(manifest)},
		"r/" + name + "/" + netregistry.ReleaseRootFile: {Data: root},
	}, "r")
	if err != nil {
		t.Fatalf("build the test registry: %v", err)
	}
	return reg
}
