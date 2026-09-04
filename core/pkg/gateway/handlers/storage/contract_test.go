package storage

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/contracttest"
)

// The body the TypeScript SDK sends to /v1/storage/pin must decode into the
// struct this handler parses, with nothing left over. See pkg/contracttest.
func TestStorageContract(t *testing.T) {
	fixtures, err := contracttest.For(".", "storage/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			switch fixture.Route {
			case "/v1/storage/pin":
				var body StoragePinRequest
				if err := fixture.DecodeStrict(&body); err != nil {
					t.Fatalf("the gateway does not parse what %s sends: %v", fixture.SDK, err)
				}
				if body.Cid == "" {
					t.Error("pin: a CID is the whole request")
				}
			default:
				t.Fatalf("fixture %s names route %s, which this test does not know", fixture.Name, fixture.Route)
			}
		})
	}
}
