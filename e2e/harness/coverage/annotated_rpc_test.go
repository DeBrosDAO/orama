package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

// An rpc with an option block (a REST annotation) ends with its own brace: the rpcs after it still
// belong to the service.
func TestChainRPCs_annotatedRPCsDoNotEndTheService(t *testing.T) {
	root := fakeRepo(t)
	doc := "package orama.token.v1;\nservice Query {\n  rpc Params(QueryParamsRequest) returns (QueryParamsResponse) {\n    option (google.api.http).get = \"/orama/token/v1/params\";\n  }\n  rpc Token(QueryTokenRequest) returns (QueryTokenResponse) {\n    option (google.api.http).get = \"/orama/token/v1/token\";\n  }\n  rpc Frozen(QueryFrozenRequest) returns (QueryFrozenResponse);\n}\nmessage QueryFrozenRequest {\n}\n"
	if err := os.WriteFile(filepath.Join(root, "chain/proto/orama/token/v1/query.proto"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := ChainRPCs(root)
	if err != nil {
		t.Fatal(err)
	}
	var queries []Item
	for _, it := range items {
		if it.Kind == KindQuery {
			queries = append(queries, it)
		}
	}
	want := "query:orama.token.v1.Params,query:orama.token.v1.Token,query:orama.token.v1.Frozen"
	if got := ids(queries); got != want {
		t.Fatalf("queries %s, want %s", got, want)
	}
}
