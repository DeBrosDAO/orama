package cache

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/contracttest"
)

// The bodies the TypeScript SDK sends to /v1/cache/* must decode into these
// request structs with nothing left over. See pkg/contracttest for why.
func TestCacheContract(t *testing.T) {
	fixtures, err := contracttest.For(".", "cache/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}
	if len(fixtures) < 4 {
		t.Fatalf("expected the cache fixtures, found %d", len(fixtures))
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			var target any
			switch fixture.Route {
			case "/v1/cache/get":
				target = &GetRequest{}
			case "/v1/cache/put":
				target = &PutRequest{}
			case "/v1/cache/delete":
				target = &DeleteRequest{}
			case "/v1/cache/mget":
				target = &MultiGetRequest{}
			case "/v1/cache/scan":
				target = &ScanRequest{}
			default:
				t.Fatalf("fixture %s names route %s, which this test does not know", fixture.Name, fixture.Route)
			}

			if err := fixture.DecodeStrict(target); err != nil {
				t.Fatalf("the gateway does not parse what %s sends: %v", fixture.SDK, err)
			}
		})
	}
}

// A cache operation without a map name or a key addresses nothing.
func TestCacheContractCarriesTheAddress(t *testing.T) {
	fixtures, err := contracttest.For(".", "cache/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}

	for _, fixture := range fixtures {
		switch fixture.Route {
		case "/v1/cache/get":
			var body GetRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.DMap == "" || body.Key == "" {
				t.Errorf("get: dmap=%q key=%q — both are required", body.DMap, body.Key)
			}
		case "/v1/cache/put":
			var body PutRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.DMap == "" || body.Key == "" {
				t.Errorf("put: dmap=%q key=%q — both are required", body.DMap, body.Key)
			}
			if body.Value == nil {
				t.Error("put: a value is the point of the call")
			}
		case "/v1/cache/delete":
			var body DeleteRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.DMap == "" || body.Key == "" {
				t.Errorf("delete: dmap=%q key=%q — both are required", body.DMap, body.Key)
			}
		case "/v1/cache/scan":
			var body ScanRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.DMap == "" {
				t.Error("scan: a dmap is required")
			}
		}
	}
}
