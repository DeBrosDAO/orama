//go:build e2e_fleet

package contractslive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
)

// contractsDir holds the request/response fixtures shared by the gateway and
// the TypeScript SDK (contracts/README.md).
const contractsDir = "contracts"

// fixture is one contracts/<area>/<name>.json with a route.
type fixture struct {
	Name     string          `json:"-"`
	Route    string          `json:"route"`
	Method   string          `json:"method"`
	SDK      string          `json:"sdk"`
	Request  map[string]any  `json:"request"`
	Response json.RawMessage `json:"response"`
}

// loadFixtures reads every route fixture, keyed "area/name". Files without a
// route (enrollment/seal.json is a crypto vector) are not route contracts.
func loadFixtures(t testing.TB) map[string]fixture {
	t.Helper()
	root := filepath.Join(cliconf.RepoRoot(t), contractsDir)
	paths, err := filepath.Glob(filepath.Join(root, "*", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures under %s: %v", root, err)
	}
	out := map[string]fixture{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if f.Route == "" {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		f.Name = strings.TrimSuffix(filepath.ToSlash(rel), ".json")
		out[f.Name] = f
	}
	return out
}

// body is the fixture's request with the given members replaced by live
// values. Only members the fixture has may be replaced: a fixture that lost
// one is a contract change this test has to follow.
func (f fixture) body(t testing.TB, live map[string]any) []byte {
	t.Helper()
	req := map[string]any{}
	for k, v := range f.Request {
		req[k] = v
	}
	for k, v := range live {
		if _, ok := req[k]; !ok {
			t.Fatalf("%s: the fixture's request has no %q member any more", f.Name, k)
		}
		req[k] = v
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// shapeDiff compares a live JSON value with the fixture's: every member the
// fixture has must be there with the same JSON type, recursively (the first
// element of arrays when both have one). Values are not compared. Members
// only the live answer has are returned apart: the SDK does not read them,
// and row objects carry whatever columns the table has.
func shapeDiff(path string, want, got any) (problems, extra []string) {
	switch w := want.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: fixture has an object, live has %s", path, kind(got))}, nil
		}
		return objectDiff(path, w, g)
	case []any:
		g, ok := got.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: fixture has an array, live has %s", path, kind(got))}, nil
		}
		if len(w) > 0 && len(g) > 0 {
			return shapeDiff(path+"[0]", w[0], g[0])
		}
		return nil, nil
	default:
		if kind(want) != kind(got) {
			return []string{fmt.Sprintf("%s: fixture has %s, live has %s", path, kind(want), kind(got))}, nil
		}
		return nil, nil
	}
}

func objectDiff(path string, w, g map[string]any) (problems, extra []string) {
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		gv, ok := g[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s.%s: in the fixture, missing live", path, k))
			continue
		}
		p, e := shapeDiff(path+"."+k, w[k], gv)
		problems, extra = append(problems, p...), append(extra, e...)
	}
	for k := range g {
		if _, ok := w[k]; !ok {
			extra = append(extra, path+"."+k)
		}
	}
	sort.Strings(extra)
	return problems, extra
}

func kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	default:
		return fmt.Sprintf("%T", v)
	}
}
