package releasepub

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func newKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestInitRootWith_aKeyOfItsOwnPerRoleAndAThreshold(t *testing.T) {
	agent := newFakeAgent(t)
	wallet, _ := agent.ReleaseKey(t.Context())
	targets, snapshot := []ed25519.PublicKey{newKey(t), newKey(t), newKey(t)}, newKey(t)
	repo := Repo{Dir: filepath.Join(t.TempDir(), "repo")}

	_, err := InitRootWith(t.Context(), agent, repo, testNow, nil, RootKeys{
		Targets:  RoleKeys{Keys: targets, Threshold: 2},
		Snapshot: RoleKeys{Keys: []ed25519.PublicKey{snapshot}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, root, err := repo.ReadRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Signed.Roles["targets"]; len(got.KeyIDs) != 3 || got.Threshold != 2 {
		t.Errorf("targets: %d keys, threshold %d; want 3 and 2", len(got.KeyIDs), got.Threshold)
	}
	if err := checkAgentKey(root, wallet, "root", "timestamp"); err != nil {
		t.Errorf("the roles left out are the wallet's: %v", err)
	}
	if err := checkAgentKey(root, wallet, "snapshot"); err == nil {
		t.Error("the snapshot role was given its own key, but the wallet still holds it")
	}
	if err := checkAgentKey(root, wallet, "targets"); err == nil {
		t.Error("the wallet holds the targets role it was not given")
	}
}

func TestInitRootWith_refusesALayoutThisCommandCannotSign(t *testing.T) {
	for name, keys := range map[string]RootKeys{
		"a root threshold of two":    {Root: RoleKeys{Keys: []ed25519.PublicKey{newKey(t), newKey(t)}, Threshold: 2}},
		"a root without the wallet":  {Root: RoleKeys{Keys: []ed25519.PublicKey{newKey(t)}}},
		"a threshold above the keys": {Targets: RoleKeys{Keys: []ed25519.PublicKey{newKey(t)}, Threshold: 2}},
		"the same key twice":         {Targets: RoleKeys{Keys: func() []ed25519.PublicKey { k := newKey(t); return []ed25519.PublicKey{k, k} }(), Threshold: 1}},
		"a key of the wrong size":    {Targets: RoleKeys{Keys: []ed25519.PublicKey{{1, 2, 3}}}},
	} {
		agent := newFakeAgent(t)
		repo := Repo{Dir: filepath.Join(t.TempDir(), "repo")}
		if _, err := InitRootWith(t.Context(), agent, repo, testNow, nil, keys); err == nil {
			t.Errorf("%s: a root was made", name)
		}
		if agent.approvals() != 0 {
			t.Errorf("%s: the wallet was asked to sign a layout that is refused", name)
		}
	}
}

func TestParseRootKeys(t *testing.T) {
	wallet := newKey(t)
	other := hex.EncodeToString(newKey(t))
	keys, err := ParseRootKeys(strings.NewReader(`{"root":{"keys":["wallet"]},"targets":{"keys":["`+other+`","wallet"],"threshold":2}}`), wallet)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.Targets.Keys) != 2 || keys.Targets.Threshold != 2 || string(keys.Root.Keys[0]) != string(wallet) || len(keys.Snapshot.Keys) != 0 {
		t.Errorf("parsed %+v", keys)
	}
	for name, doc := range map[string]string{
		"an unknown field": `{"rooot":{}}`,
		"a short key":      `{"root":{"keys":["abcd"]}}`,
		"a second value":   `{} {}`,
		"not json":         `root`,
		"too large":        `{"root":{"keys":["` + strings.Repeat("a", maxRootKeysFileBytes) + `"]}}`,
	} {
		if _, err := ParseRootKeys(strings.NewReader(doc), wallet); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if keys, err := ParseRootKeys(strings.NewReader(`{}`), wallet); err != nil || keys.Root.Keys != nil {
		t.Errorf("an empty file is today's layout: %+v, %v", keys, err)
	}
}
