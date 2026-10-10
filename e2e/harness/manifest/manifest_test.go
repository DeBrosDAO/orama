package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `id: smoke
title: Smoke
area: platform
subtasks: [2819]
stage: 1
destructive: false
requires: {extra_nodes: 0, probe: false, chain: false}
covers:
  cli: ["orama version"]
  routes: ["/health", "/v1/status"]
  msgs: ["orama.token.v1.MsgCreateToken"]
  queries: ["orama.token.v1.Params"]
  units: ["orama-namespace-rqlite@.service", "orama-turn.service"]
  config: ["node.yaml:gateway.base_domain"]
  claims: ["docs/whitepaper/technical-reference/vol1/14-authorization.md#signing-in: the nonce is single-use"]
`

func writeFeature(t *testing.T, root, dir, body string) {
	t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAll_valid(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "smoke", validYAML)
	ms, err := LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ID != "smoke" || ms[0].Dir != "smoke" || ms[0].Stage != 1 {
		t.Fatalf("got %+v", ms)
	}
	ids := ms[0].Covers.IDs()
	for _, want := range []string{"cli:orama version", "route:/health", "msg:orama.token.v1.MsgCreateToken",
		"query:orama.token.v1.Params", "unit:orama-turn.service", "config:node.yaml:gateway.base_domain"} {
		found := false
		for _, id := range ids {
			found = found || id == want
		}
		if !found {
			t.Errorf("IDs() lacks %s: %v", want, ids)
		}
	}
}

func TestLoadAll_emptyDir(t *testing.T) {
	ms, err := LoadAll(t.TempDir())
	if err != nil || len(ms) != 0 {
		t.Fatalf("ms=%v err=%v", ms, err)
	}
}

func TestParse_unknownKeyRejected(t *testing.T) {
	if _, err := Parse([]byte(validYAML + "cover: {}\n")); err == nil {
		t.Fatal("unknown top-level key accepted")
	}
	bad := strings.Replace(validYAML, "  cli:", "  commands:", 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("unknown covers key accepted")
	}
	if _, err := Parse([]byte(validYAML + "---\nid: x\n")); err == nil {
		t.Fatal("second YAML document accepted")
	}
}

func TestLoadAll_duplicateIDsImpossible(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "smoke", validYAML)
	writeFeature(t, root, "smoke2", strings.Replace(validYAML, "id: smoke", "id: smoke2", 1))
	ms, err := LoadAll(root)
	if err != nil || len(ms) != 2 || ms[0].ID != "smoke" || ms[1].ID != "smoke2" {
		t.Fatalf("ms=%v err=%v", ms, err)
	}
	// A second directory claiming an existing id fails the directory check.
	writeFeature(t, root, "copy", validYAML)
	if _, err := LoadAll(root); err == nil {
		t.Fatal("a copied manifest with a duplicate id was accepted")
	}
}

func TestValidate_problems(t *testing.T) {
	cases := map[string]string{
		"dir mismatch":    strings.Replace(validYAML, "id: smoke", "id: other", 1),
		"bad id":          strings.Replace(validYAML, "id: smoke", "id: Smoke_1", 1),
		"no title":        strings.Replace(validYAML, "title: Smoke", "title: ''", 1),
		"stage zero":      strings.Replace(validYAML, "stage: 1", "stage: 0", 1),
		"stage too high":  strings.Replace(validYAML, "stage: 1", "stage: 12", 1),
		"extra nodes":     strings.Replace(validYAML, "extra_nodes: 0", "extra_nodes: 9", 1),
		"negative task":   strings.Replace(validYAML, "[2819]", "[-1]", 1),
		"dup task":        strings.Replace(validYAML, "[2819]", "[2819, 2819]", 1),
		"cli not orama":   strings.Replace(validYAML, `"orama version"`, `"ls -la"`, 1),
		"route no slash":  strings.Replace(validYAML, `"/health"`, `"health"`, 1),
		"msg lowercase":   strings.Replace(validYAML, "MsgCreateToken", "createToken", 1),
		"unit wrong":      strings.Replace(validYAML, "orama-turn.service", "sshd.service", 1),
		"config no key":   strings.Replace(validYAML, "node.yaml:gateway.base_domain", "node.yaml", 1),
		"claim not doc":   strings.Replace(validYAML, "docs/whitepaper/technical-reference/vol1/14-authorization.md#signing-in", "it works", 1),
		"duplicate route": strings.Replace(validYAML, `"/v1/status"`, `"/health"`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFeature(t, root, "smoke", body)
			if _, err := LoadAll(root); err == nil {
				t.Fatalf("accepted:\n%s", body)
			}
		})
	}
}

func TestValidate_emptyCovers(t *testing.T) {
	m := Manifest{ID: "x", Title: "X", Area: "a", Stage: 1}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "covers is empty") {
		t.Fatalf("got %v", err)
	}
}

func TestValidate_reportsAllProblemsAtOnce(t *testing.T) {
	m := Manifest{ID: "BAD", Stage: 99}
	err := m.Validate()
	if err == nil {
		t.Fatal("invalid manifest accepted")
	}
	for _, want := range []string{"id", "title", "area", "stage", "covers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestClaimShape_forms(t *testing.T) {
	accepted := []string{
		"website/src/docs/developer/webrtc.mdx#firewall: only 443 is open",
		"website/src/docs/operator/troubleshooting.mdx",
		"docs/whitepaper/technical-reference/vol1/14-authorization.md#error-codes",
		"docs/whitepaper/technical-reference/appendices/i-api-surface.md: every route has an owner",
		"plans/e2e-fleet.md",
	}
	rejected := []string{
		"website/src/docs/developer/webrtc.md",
		"website/src/docs/webrtc.mdx",
		"website/src/pages/docs.tsx",
		"website/src/docs/developer/Webrtc.mdx",
		"website/docs/developer/webrtc.mdx",
		"docs/whitepaper/technical-reference/vol1/x.mdx",
		"README.md",
		"it works",
	}
	for _, c := range accepted {
		if !claimShape.MatchString(c) {
			t.Errorf("claim %q was refused", c)
		}
	}
	for _, c := range rejected {
		if claimShape.MatchString(c) {
			t.Errorf("claim %q was accepted", c)
		}
	}
}
