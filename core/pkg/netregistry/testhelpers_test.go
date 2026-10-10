package netregistry

import (
	"encoding/json"
	"testing"
)

// testRoot and testGenesis stand in for release-root.json and genesis.json;
// the registry treats the root as opaque bytes and reads only the genesis's
// chain_id.
var (
	testRoot    = []byte(`{"signed":{"_type":"root"}}`)
	testGenesis = []byte(`{"chain_id":"orama-teststage-1","app_state":{}}`)
)

func validManifest() Manifest {
	return Manifest{
		Name:              "teststage",
		ChainID:           "orama-teststage-1",
		GenesisSHA256:     Digest(testGenesis),
		Seeds:             []string{"seed1.teststage.example.org", "seed2.teststage.example.org"},
		Channel:           ChannelNightly,
		MinVersion:        "0.3.0",
		ReleaseRepo:       "https://releases.example.org/",
		ReleaseRootSHA256: Digest(testRoot),
		Faucet:            true,
	}
}

func marshalManifest(t *testing.T, m Manifest) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
