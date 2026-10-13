package namespace

import (
	"encoding/json"
	"testing"
)

// The coordinator sends "purge_data" on the delete's teardown-namespace; a
// renamed field on either side would silently turn deletes into plain teardowns.
func TestSpawnRequest_decodesPurgeData(t *testing.T) {
	var req SpawnRequest
	if err := json.Unmarshal([]byte(`{"action":"teardown-namespace","namespace":"acme","purge_data":true}`), &req); err != nil {
		t.Fatal(err)
	}
	if !req.PurgeData {
		t.Fatal("purge_data was not decoded")
	}
	var plain SpawnRequest
	if err := json.Unmarshal([]byte(`{"action":"teardown-namespace","namespace":"acme"}`), &plain); err != nil || plain.PurgeData {
		t.Fatalf("a request without purge_data purges: %+v, %v", plain, err)
	}
}
