package installers

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIpfsStorageMaxForDisk verifies the disk-aware StorageMax sizing policy:
// 50% of total disk, floored at the kubo default of 10GB. Heterogeneous node
// disks are why this is computed rather than hardcoded (see ipfs.go).
func TestIpfsStorageMaxForDisk(t *testing.T) {
	tests := []struct {
		name       string
		totalBytes uint64
		want       string
	}{
		{"290GB disk -> 50%", 290_000_000_000, "145GB"},
		{"96GB disk -> 50%", 96_000_000_000, "48GB"},
		{"145GB disk -> 50% truncates", 145_000_000_000, "72GB"},
		{"tiny disk floored at 10GB", 8_000_000_000, "10GB"},
		{"floor boundary (20GB disk -> 10GB)", 20_000_000_000, "10GB"},
		{"zero bytes floored", 0, "10GB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ipfsStorageMaxForDisk(tt.totalBytes); got != tt.want {
				t.Errorf("ipfsStorageMaxForDisk(%d) = %q, want %q", tt.totalBytes, got, tt.want)
			}
		})
	}
}

// TestSetDatastoreStorageMax_preservesOtherFields ensures we only touch
// Datastore.StorageMax and leave every other config field intact — critical,
// since the IPFS config carries the node Identity, swarm Addresses, and other
// Datastore knobs (GCPeriod, StorageGCWatermark) that must not be clobbered.
func TestSetDatastoreStorageMax_preservesOtherFields(t *testing.T) {
	cfg := map[string]interface{}{
		"Datastore": map[string]interface{}{
			"StorageMax":         "10GB",
			"StorageGCWatermark": float64(90),
			"GCPeriod":           "1h",
			"Spec":               map[string]interface{}{"type": "mount"},
		},
		"Addresses": map[string]interface{}{
			"API": []interface{}{"/ip4/127.0.0.1/tcp/4501"},
		},
		"Identity": map[string]interface{}{"PeerID": "12D3KooBogus"},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	out, err := setDatastoreStorageMax(data, "145GB")
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	ds, ok := got["Datastore"].(map[string]interface{})
	if !ok {
		t.Fatal("Datastore section missing after update")
	}
	if ds["StorageMax"] != "145GB" {
		t.Errorf("StorageMax = %v, want 145GB", ds["StorageMax"])
	}
	if ds["GCPeriod"] != "1h" {
		t.Errorf("GCPeriod = %v, want preserved 1h", ds["GCPeriod"])
	}
	if ds["StorageGCWatermark"] != float64(90) {
		t.Errorf("StorageGCWatermark = %v, want preserved 90", ds["StorageGCWatermark"])
	}
	if _, ok := got["Identity"].(map[string]interface{}); !ok {
		t.Error("Identity section lost — would orphan the node's PeerID")
	}
	if _, ok := got["Addresses"].(map[string]interface{}); !ok {
		t.Error("Addresses section lost")
	}
}

// TestSetDatastoreStorageMax_createsDatastoreWhenMissing covers a config with no
// Datastore section (defensive — kubo always writes one, but the helper must not
// panic on a nil map).
func TestSetDatastoreStorageMax_createsDatastoreWhenMissing(t *testing.T) {
	out, err := setDatastoreStorageMax([]byte(`{"Addresses":{"API":["x"]}}`), "50GB")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	ds, ok := got["Datastore"].(map[string]interface{})
	if !ok || ds["StorageMax"] != "50GB" {
		t.Errorf("expected Datastore.StorageMax=50GB created, got %v", got["Datastore"])
	}
}

// TestSetDatastoreStorageMax_invalidJSON ensures a malformed config surfaces an
// error rather than silently writing garbage.
func TestSetDatastoreStorageMax_invalidJSON(t *testing.T) {
	if _, err := setDatastoreStorageMax([]byte("not json"), "10GB"); err == nil {
		t.Error("expected error on invalid JSON, got nil")
	}
}

// TestConfigureDatastore_writesValidStorageMax exercises the full method against
// a real temp config + the test host's filesystem (real syscall.Statfs). It
// asserts a valid NNGB value is written and unrelated fields survive — without
// pinning to an exact number, since CI disk size varies.
func TestConfigureDatastore_writesValidStorageMax(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	orig := `{
  "Datastore": {"StorageMax": "10GB", "GCPeriod": "1h", "StorageGCWatermark": 90},
  "Identity": {"PeerID": "keepme"}
}`
	if err := os.WriteFile(configPath, []byte(orig), 0600); err != nil {
		t.Fatal(err)
	}

	ii := NewIPFSInstaller("amd64", io.Discard)
	if err := ii.configureDatastore(dir); err != nil {
		t.Fatalf("configureDatastore: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config not valid JSON after configureDatastore: %v", err)
	}
	ds, ok := got["Datastore"].(map[string]interface{})
	if !ok {
		t.Fatal("Datastore section missing")
	}
	sm, _ := ds["StorageMax"].(string)
	if !strings.HasSuffix(sm, "GB") {
		t.Errorf("StorageMax = %q, want an NNGB value", sm)
	}
	if ds["GCPeriod"] != "1h" {
		t.Errorf("GCPeriod not preserved: %v", ds["GCPeriod"])
	}
	id, _ := got["Identity"].(map[string]interface{})
	if id == nil || id["PeerID"] != "keepme" {
		t.Error("Identity.PeerID not preserved")
	}
}

// TestConfigureDatastore_missingConfig surfaces a clear error when the repo
// config does not exist (rather than statfs succeeding and a later silent fail).
func TestConfigureDatastore_missingConfig(t *testing.T) {
	dir := t.TempDir() // exists for statfs, but has no "config" file
	ii := NewIPFSInstaller("amd64", io.Discard)
	if err := ii.configureDatastore(dir); err == nil {
		t.Error("expected error when IPFS config file is missing, got nil")
	}
}
