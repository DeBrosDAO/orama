package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFeatures_contract lints the real features directory: this is the check
// `make e2e-lint` and `make test` run.
func TestFeatures_contract(t *testing.T) {
	problems, err := Check(filepath.Join("..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

const goodManifest = "id: good\ntitle: Good\narea: platform\nstage: 1\ncovers:\n  routes: [\"/health\"]\n"

const goodMain = `//go:build e2e_fleet

package good

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }
`

const goodTest = `//go:build e2e_fleet

package good

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

func TestHealth_ok(t *testing.T) {
	harness.SkipNotApplicable(t, "reason")
}

func helper(t *testing.T) string { return "" }
`

func feature(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "good")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func problems(t *testing.T, files map[string]string) string {
	t.Helper()
	ps, err := Check(feature(t, files))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return strings.Join(out, "\n")
}

func base() map[string]string {
	return map[string]string{"feature.yaml": goodManifest, "main_test.go": goodMain, "x_test.go": goodTest}
}

func TestCheck_goodFeaturePasses(t *testing.T) {
	if got := problems(t, base()); got != "" {
		t.Fatalf("problems in a good feature:\n%s", got)
	}
}

func TestCheck_violations(t *testing.T) {
	cases := map[string]struct {
		file, body, want string
	}{
		"sleep":          {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"time\"\n\nfunc h() { time.Sleep(1) }\n", "time.Sleep"},
		"aliased sleep":  {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport tm \"time\"\n\nfunc h() { tm.Sleep(1) }\n", "time.Sleep"},
		"time after":     {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"time\"\n\nfunc h() { <-time.After(1) }\n", "time.After"},
		"new timer":      {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"time\"\n\nfunc h() { time.NewTimer(1) }\n", "time.NewTimer"},
		"tick":           {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"time\"\n\nfunc h() { <-time.Tick(1) }\n", "time.Tick"},
		"new ticker":     {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"time\"\n\nfunc h() { time.NewTicker(1) }\n", "time.NewTicker"},
		"after func":     {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"time\"\n\nfunc h() { time.AfterFunc(1, nil) }\n", "time.AfterFunc"},
		"always error":   {"y_test.go", "//go:build e2e_fleet\n\npackage good\n\nimport \"fmt\"\n\nfunc h(n int) (bool, error) { return n == 2, fmt.Errorf(\"%d\", n) }\n", "poll closure must return true, nil"},
		"bare skip":      {"x_test.go", strings.Replace(goodTest, `harness.SkipNotApplicable(t, "reason")`, `t.Skip("later")`, 1), "bare Skip"},
		"skipnow":        {"x_test.go", strings.Replace(goodTest, `harness.SkipNotApplicable(t, "reason")`, `t.SkipNow()`, 1), "bare SkipNow"},
		"no tag":         {"x_test.go", strings.Replace(goodTest, "//go:build e2e_fleet\n", "", 1), "missing //go:build"},
		"wrong tag":      {"x_test.go", strings.Replace(goodTest, "e2e_fleet", "e2e", 1), "does not require"},
		"or tag":         {"x_test.go", strings.Replace(goodTest, "e2e_fleet", "e2e_fleet || linux", 1), "does not require"},
		"bad name":       {"x_test.go", strings.Replace(goodTest, "TestHealth_ok", "TestHealth", 1), "Test{Function}_{scenario}"},
		"not run":        {"x_test.go", goodTest + "\nfunc HealthCheck(t *testing.T) {}\n", "never runs"},
		"lower not run":  {"x_test.go", goodTest + "\nfunc testHealth(t *testing.T) {}\n", "never runs"},
		"bad signature":  {"x_test.go", goodTest + "\nfunc TestX_y(t *testing.T, n int) {}\n", "signature"},
		"no main":        {"main_test.go", strings.Replace(goodMain, "harness.Main(m)", "m.Run()", 1), "no TestMain"},
		"no manifest":    {"feature.yaml", "id: other\ntitle: t\narea: a\nstage: 1\ncovers:\n  routes: [\"/x\"]\n", "manifest"},
		"does not parse": {"z_test.go", "//go:build e2e_fleet\n\npackage good\nfunc {", "does not parse"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			files := base()
			files[c.file] = c.body
			if got := problems(t, files); !strings.Contains(got, c.want) {
				t.Fatalf("want a problem containing %q, got:\n%s", c.want, got)
			}
		})
	}
}

func TestCheck_emptyFeature(t *testing.T) {
	got := problems(t, map[string]string{})
	for _, want := range []string{"manifest", "no TestMain", "no Test functions"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty feature: missing %q in:\n%s", want, got)
		}
	}
}

func TestCheck_missingDir(t *testing.T) {
	if _, err := Check(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("missing features dir accepted")
	}
}

func internalTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, InternalDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const goodHelper = "//go:build e2e_fleet\n\npackage nsutil\n\nfunc Name() string { return \"x\" }\n"

// TestCheck_internalHelpersAreNotFeatures: shared helper packages need no
// manifest, TestMain or tests, at any depth.
func TestCheck_internalHelpersAreNotFeatures(t *testing.T) {
	ps, err := Check(internalTree(t, map[string]string{"nsutil/nsutil.go": goodHelper, "a/b/deep.go": strings.Replace(goodHelper, "nsutil", "b", 1)}))
	if err != nil || len(ps) != 0 {
		t.Fatalf("problems %v err %v", ps, err)
	}
}

func TestCheck_internalHelpersStillLinted(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"no tag": {strings.Replace(goodHelper, "//go:build e2e_fleet\n", "", 1), "missing //go:build"},
		"sleep":  {"//go:build e2e_fleet\n\npackage nsutil\n\nimport \"time\"\n\nfunc W() { time.Sleep(1) }\n", "time.Sleep"},
		"after":  {"//go:build e2e_fleet\n\npackage nsutil\n\nimport \"time\"\n\nfunc W() { <-time.After(1) }\n", "time.After"},
		"skip":   {"//go:build e2e_fleet\n\npackage nsutil\n\nimport \"testing\"\n\nfunc W(t *testing.T) { t.Skip() }\n", "bare Skip"},
		"parse":  {"//go:build e2e_fleet\n\npackage nsutil\nfunc {", "does not parse"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ps, err := Check(internalTree(t, map[string]string{"nsutil/x.go": c.body}))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range ps {
				got = append(got, p.String())
			}
			if !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Fatalf("want %q, got %v", c.want, got)
			}
		})
	}
}
