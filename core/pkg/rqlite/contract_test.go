package rqlite

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/contracttest"
)

// The request bodies the TypeScript SDK sends to /v1/rqlite/* must decode into
// the structs these handlers parse, with nothing left over. An unknown field
// means the SDK is sending something this gateway silently drops — which is how
// a renamed field goes unnoticed until it reaches production.
func TestDatabaseContract(t *testing.T) {
	fixtures, err := contracttest.For(".", "db/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}
	if len(fixtures) < 7 {
		t.Fatalf("expected the db fixtures, found %d", len(fixtures))
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			var target any
			switch fixture.Route {
			case "/v1/rqlite/query":
				target = &queryRequest{}
			case "/v1/rqlite/exec":
				target = &execRequest{}
			case "/v1/rqlite/find", "/v1/rqlite/find-one":
				target = &findRequest{}
			case "/v1/rqlite/select":
				target = &selectRequest{}
			case "/v1/rqlite/transaction":
				target = &transactionRequest{}
			case "/v1/rqlite/create-table":
				// The handler decodes into an anonymous struct; this mirrors it.
				target = &struct {
					Schema string `json:"schema"`
				}{}
			case "/v1/rqlite/drop-table":
				target = &struct {
					Table string `json:"table"`
				}{}
			default:
				t.Fatalf("fixture %s names route %s, which this test does not know", fixture.Name, fixture.Route)
			}

			if err := fixture.DecodeStrict(target); err != nil {
				t.Fatalf("the gateway does not parse what %s sends: %v", fixture.SDK, err)
			}
		})
	}
}

// The fields the handler needs must actually arrive, not merely decode.
func TestDatabaseContractCarriesTheRequiredFields(t *testing.T) {
	fixtures, err := contracttest.For(".", "db/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}

	for _, fixture := range fixtures {
		switch fixture.Route {
		case "/v1/rqlite/query":
			var body queryRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.SQL == "" {
				t.Error("query: the handler rejects an empty sql, so the contract must carry one")
			}
		case "/v1/rqlite/exec":
			var body execRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.SQL == "" {
				t.Error("exec: the handler rejects an empty sql")
			}
		case "/v1/rqlite/find", "/v1/rqlite/find-one":
			var body findRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.Table == "" {
				t.Errorf("%s: the handler rejects an empty table", fixture.Name)
			}
		case "/v1/rqlite/transaction":
			var body transactionRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Ops) == 0 && len(body.Statements) == 0 {
				t.Error("transaction: the handler needs ops or statements")
			}
		}
	}
}
