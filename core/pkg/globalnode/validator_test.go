package globalnode

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"golang.org/x/crypto/nacl/box"
)

func validatorKeyJSON(t *testing.T) []byte {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"address":  "ABCDEF",
		"pub_key":  map[string]string{"type": "tendermint/PubKeyEd25519", "value": base64.StdEncoding.EncodeToString(pub)},
		"priv_key": map[string]string{"type": "tendermint/PrivKeyEd25519", "value": base64.StdEncoding.EncodeToString(priv)},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func stateJSON(height string, round, step int) []byte {
	return []byte(`{"height":"` + height + `","round":` + strconv.Itoa(round) + `,"step":` + strconv.Itoa(step) + `,"signature":"c2ln","signbytes":"AA=="}`)
}

// newHost is a chain home under a temporary anchor, initialised as oramad
// init leaves it: a key and a state at height 0.
func newHost(t *testing.T) Host {
	t.Helper()
	anchor := t.TempDir()
	stateDir := filepath.Join(anchor, "orama-global")
	home := filepath.Join(stateDir, "chain")
	for _, dir := range []string{filepath.Join(home, "config"), filepath.Join(home, "data")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := Host{
		Root:      rootfs.At(anchor),
		StateDir:  stateDir,
		KeyPath:   filepath.Join(home, "config", "priv_validator_key.json"),
		StatePath: filepath.Join(home, "data", "priv_validator_state.json"),
		Lookup:    func(string) (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Chown:     func(r rootfs.Root, path string, uid, gid int) error { return r.Chown(path, uid, gid) },
		Now:       func() time.Time { return time.Unix(1_800_000_000, 0) },
	}
	write(t, h.KeyPath, validatorKeyJSON(t))
	write(t, h.StatePath, emptySignState)
	return h
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExportKey_roundTripsThroughTheOperatorKey(t *testing.T) {
	h := newHost(t)
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := h.ExportKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, read(t, h.KeyPath)[:20]) {
		t.Fatal("the export contains the key in the clear")
	}
	opened, err := nsbackup.Open(priv, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, read(t, h.KeyPath)) {
		t.Fatal("the opened export is not priv_validator_key.json")
	}
	_, other, _ := box.GenerateKey(rand.Reader)
	if _, err := nsbackup.Open(other, sealed); err == nil {
		t.Fatal("another key opened the export")
	}
}

func TestExportKey_refusesAMalformedKey(t *testing.T) {
	h := newHost(t)
	write(t, h.KeyPath, []byte(`{"pub_key":{"type":"tendermint/PubKeySecp256k1","value":"AA=="}}`))
	pub, _, _ := box.GenerateKey(rand.Reader)
	if _, err := h.ExportKey(pub); err == nil {
		t.Fatal("a non-ed25519 key was exported")
	}
}

func TestExportKey_missingKeyIsAnError(t *testing.T) {
	h := newHost(t)
	if err := os.Remove(h.KeyPath); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := box.GenerateKey(rand.Reader)
	if _, err := h.ExportKey(pub); err == nil {
		t.Fatal("export succeeded with no key")
	}
}

func TestCheckSignFloor_noMigrationNoFloor(t *testing.T) {
	if err := newHost(t).CheckSignFloor(); err != nil {
		t.Fatal(err)
	}
}
