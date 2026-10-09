package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
)

const testProto = `syntax = "proto3";
package orama.demo.v1;

// Msg is the demo message service.
service Msg {
  // Create makes a thing.
  rpc Create(MsgCreate) returns (MsgCreateResponse);
  rpc Remove(MsgRemove) returns (MsgRemoveResponse);
}

service Query {
  rpc Thing(QueryThingRequest) returns (QueryThingResponse) {
    option (google.api.http).get = "/orama/demo/v1/thing/{id}";
  }
}

// MsgRemove deletes a thing; only its owner may.
message MsgRemove {
  string owner = 1;
  repeated uint64 ids = 2;
  message Nested {
    string inner = 1;
  }
}

message MsgCreate {
  string owner = 1 [(gogoproto.nullable) = false];
}

message QueryThingRequest {
  uint64 id = 1;
}
`

func TestParseProto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tx.proto")
	if err := os.WriteFile(path, []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	pf, err := parseProto(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.rpcs) != 3 {
		t.Fatalf("want 3 rpcs, got %+v", pf.rpcs)
	}
	byName := map[string]protoRPC{}
	for _, r := range pf.rpcs {
		byName[r.name] = r
	}
	if r := byName["Create"]; r.service != "Msg" || r.comment != "Create makes a thing." || r.request != "MsgCreate" {
		t.Errorf("Create parsed as %+v", r)
	}
	if r := byName["Thing"]; r.service != "Query" || r.path != "/orama/demo/v1/thing/{id}" {
		t.Errorf("Thing parsed as %+v", r)
	}
	rm := pf.messages["MsgRemove"]
	if rm.comment != "MsgRemove deletes a thing; only its owner may." {
		t.Errorf("MsgRemove comment = %q", rm.comment)
	}
	if got := strings.Join(rm.fields, ","); !strings.HasPrefix(got, "owner string,ids repeated uint64") {
		t.Errorf("MsgRemove fields = %q", got)
	}
}

func TestParseProto_missingFile(t *testing.T) {
	if _, err := parseProto(filepath.Join(t.TempDir(), "none.proto")); err == nil {
		t.Fatal("want an error for a missing proto")
	}
}

func TestReadPlacement(t *testing.T) {
	src := `package rqlite
var tablePlacement = map[string]tableNote{
	"api_keys": {PlacementCluster, TrustPlatform, "a key is validated against the registry"},
	"functions": {PlacementNamespace, TrustTenantData, "tenant code"},
}
`
	path := filepath.Join(t.TempDir(), "p.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, err := readPlacement(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := notes["api_keys"]; n.placement != "Cluster" || n.trust != "Platform" || n.why != "a key is validated against the registry" {
		t.Errorf("api_keys = %+v", n)
	}
	if n := notes["functions"]; n.placement != "Namespace" || n.trust != "TenantData" {
		t.Errorf("functions = %+v", n)
	}
}

func TestReadPlacement_noEntriesIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.go")
	if err := os.WriteFile(path, []byte("package rqlite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlacement(path); err == nil {
		t.Fatal("want an error when the map has changed shape")
	}
}

func TestSectionBodyAndRebase(t *testing.T) {
	doc := "# Cache\n\n## Lifecycle\n\nx\n\n## Known gaps\n\n### Eviction\n\n- No pruning; see [the model](#the-model) and [mesh](06-the-wireguard-mesh.md#join).\n\n```\n## not a heading\n```\n\n## Verify it yourself\n\ny\n"
	body := sectionBody(splitLines(doc), "Known gaps")
	if !strings.HasPrefix(body, "**Eviction**") || strings.Contains(body, "Verify it yourself") || !strings.Contains(body, "## not a heading") {
		t.Fatalf("section body = %q", body)
	}
	got := rebaseLinks(body, "vol1/18-cache.md")
	for _, want := range []string{"(../vol1/18-cache.md#the-model)", "(../vol1/06-the-wireguard-mesh.md#join)"} {
		if !strings.Contains(got, want) {
			t.Errorf("rebased body lacks %s: %q", want, got)
		}
	}
	if sectionBody(splitLines("# T\n\n## Other\n"), "Known gaps") != "" {
		t.Error("want an empty body when the section is absent")
	}
}

func TestTableCell(t *testing.T) {
	got := tableCell("Port  for a|b\n  with {braces} and <ns>")
	if got != "Port for a\\|b with &#123;braces&#125; and &lt;ns>" {
		t.Errorf("tableCell = %q", got)
	}
	if strings.ContainsAny(got, "{}<") {
		t.Errorf("tableCell left an MDX-unsafe character: %q", got)
	}
}

func routeTestTable() *routepolicy.Table {
	t := routepolicy.NewTable()
	t.Add(routepolicy.Policy{Access: routepolicy.Open}, "/health")
	t.Add(routepolicy.Policy{Domain: "deploy", Action: "write", Ownership: true, MainGateway: true, Token: routepolicy.WalletToken}, "/v1/deployments/delete")
	t.AddDynamic("/v1/storage/unpin/", func(*http.Request) routepolicy.Policy { return routepolicy.Policy{} })
	return t
}

func TestRenderRoutes(t *testing.T) {
	handlers := map[string]string{"/health": "g.healthHandler"}
	out, err := renderRoutes(routeTestTable(), handlers, "Gateway routes")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"| `/health` | `g.healthHandler` | open |  | any |  |",
		"| `/v1/deployments/delete` | - | credential | `deploy:write` | wallet | owned, main |",
		"| `/v1/storage/unpin/` | - | by request |",
		"## /v1/deployments",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered routes lack %q:\n%s", want, got)
		}
	}
}

func TestRenderRoutes_undescribedDynamicRoute(t *testing.T) {
	table := routepolicy.NewTable()
	table.AddDynamic("/v1/new/", func(*http.Request) routepolicy.Policy { return routepolicy.Policy{} })
	if _, err := renderRoutes(table, nil, "Gateway routes"); err == nil {
		t.Fatal("want an error for a dynamic route with no note")
	}
}

func TestMuxHandlersFromSource(t *testing.T) {
	src := `package p
func f() {
	mux.HandleFunc("/a", g.aHandler)
	mux.HandleFunc("/b", g.withHomeNodeOnly(g.h.B))
	mux.Handle("/c", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	mux.HandleFunc(dynamic, g.skipped)
}`
	got, err := muxHandlersFromSource("p.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"/a": "g.aHandler", "/b": "g.h.B", "/c": "inline handler"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}

func TestMuxHandlersFromSource_syntaxError(t *testing.T) {
	if _, err := muxHandlersFromSource("p.go", []byte("package")); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestGenRoutes_coversTheRealTable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, err := genRoutes(&Book{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	table := gateway.RoutePolicies()
	for pattern := range dynamicNotes {
		if !table.Declared(pattern) {
			t.Errorf("dynamicNotes describes %s, which the policy table does not declare", pattern)
		}
	}
	for _, pattern := range table.Patterns() {
		if !strings.Contains(string(out), "`"+pattern+"`") {
			t.Errorf("route %s is missing from Appendix C", pattern)
		}
	}
}

func TestSameDocument_keepsOnlyFilesOfTheSamePDF(t *testing.T) {
	keys := map[string]string{"vol1/a.md": "ch01", "vol1/b.md": "ch02", "vol2/c.md": "ch03", "appendices/x.md": "appA"}
	groups := map[string]string{"vol1/a.md": "vol1", "vol1/b.md": "vol1", "vol2/c.md": "vol2", "appendices/x.md": "appendices"}
	got := sameDocument(keys, groups, "vol1/a.md")
	if len(got) != 2 || got["vol1/b.md"] != "ch02" {
		t.Fatalf("sameDocument(vol1/a.md) = %v, want only the two volume I files", got)
	}
	if _, ok := got["vol2/c.md"]; ok {
		t.Fatal("a link into another volume must not get a label")
	}
}

func TestSameDocument_unknownFile(t *testing.T) {
	got := sameDocument(map[string]string{"a.md": "ch01"}, map[string]string{"a.md": "vol1"}, "zzz.md")
	if len(got) != 0 {
		t.Fatalf("an unlisted file shares a PDF with nothing, got %v", got)
	}
}
