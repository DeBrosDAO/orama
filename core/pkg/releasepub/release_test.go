package releasepub

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

func TestInitRoot_listsTheWalletKeyForAllFourRolesAndSignsOnce(t *testing.T) {
	agent := newFakeAgent(t)
	repo := Repo{Dir: filepath.Join(t.TempDir(), "repo")}

	digest, err := InitRoot(t.Context(), agent, repo, testNow, nil)
	if err != nil {
		t.Fatal(err)
	}
	if agent.approvals() != 1 {
		t.Fatalf("approvals = %d, want 1", agent.approvals())
	}
	data, root, err := repo.ReadRoot()
	if err != nil {
		t.Fatal(err)
	}
	if digest != releaseverify.RootDigest(data) {
		t.Fatalf("digest %s is not the root's", digest)
	}
	pub, _ := agent.ReleaseKey(t.Context())
	for _, role := range []string{"root", "timestamp", "snapshot", "targets"} {
		if err := checkAgentKey(root, pub, role); err != nil {
			t.Errorf("%s: %v", role, err)
		}
		if root.Signed.Roles[role].Threshold != 1 {
			t.Errorf("%s threshold = %d", role, root.Signed.Roles[role].Threshold)
		}
	}
	first, err := os.ReadFile(filepath.Join(repo.Dir, "1.root.json"))
	if err != nil || !bytes.Equal(first, data) {
		t.Fatalf("1.root.json differs from root.json: %v", err)
	}
	if want := testNow.Add(RootValidity); !root.Signed.Expires.Equal(want) {
		t.Fatalf("root expires %s, want %s", root.Signed.Expires, want)
	}
}

func TestInitRoot_refusesASecondRootAndASignatureThatIsDenied(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if _, err := InitRoot(t.Context(), agent, repo, testNow, nil); err == nil {
		t.Fatal("a second root was made over the first")
	}
	denied := newFakeAgent(t)
	denied.refuseAt, denied.refuseErr = 1, errors.New("denied in the desktop app")
	fresh := Repo{Dir: filepath.Join(t.TempDir(), "r")}
	if _, err := InitRoot(t.Context(), denied, fresh, testNow, nil); err == nil {
		t.Fatal("a root was written without an approval")
	}
	if _, err := os.Stat(filepath.Join(fresh.Dir, "root.json")); err == nil {
		t.Fatal("a refused approval left a root behind")
	}
}

func TestRenewRoot_isTheNextVersionAClientHoldingTheOldRootAccepts(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	oldData, _, _ := repo.ReadRoot()

	version, err := RenewRoot(t.Context(), agent, repo, testNow.Add(180*24*time.Hour), nil)
	if err != nil || version != 2 {
		t.Fatalf("version %d, err %v", version, err)
	}
	if err := checkRotation(oldData, mustRead(t, filepath.Join(repo.Dir, "2.root.json"))); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(repo.Dir, "root.json")), mustRead(t, filepath.Join(repo.Dir, "2.root.json"))) {
		t.Fatal("root.json is not the newest version")
	}
	if _, err := RenewRoot(t.Context(), agent, repo, testNow, nil); err == nil {
		t.Fatal("a renewal that does not extend the expiry was made")
	}
}

func TestRenewRoot_refusesAWalletWhoseKeyIsNotTheRoots(t *testing.T) {
	repo := newRepo(t, newFakeAgent(t))
	if _, err := RenewRoot(t.Context(), newFakeAgent(t), repo, testNow.Add(24*time.Hour), nil); err == nil {
		t.Fatal("another wallet renewed the root")
	}
}

func TestCut_aFirstReleaseIsThreeApprovalsAndAClientAcceptsIt(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	before := agent.approvals()
	var progress bytes.Buffer
	arm := archive(t, "0.3.1", "arm64", "arm64 bytes")
	p := cutParams(repo, agent, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "amd64 bytes"), arm)
	p.Progress = &progress

	plan, err := Cut(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got := agent.approvals() - before; got != 3 {
		t.Fatalf("approvals = %d, want 3", got)
	}
	if plan.Tag != "release-nightly-0.3.1" || len(plan.Added) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	for _, want := range []string{"Approval 1 of 3: targets.json version 1", "Approval 2 of 3: snapshot.json", "Approval 3 of 3: timestamp.json"} {
		if !strings.Contains(progress.String(), want) {
			t.Errorf("progress lacks %q:\n%s", want, progress.String())
		}
	}
	verified, err := clientView(t, repo, testNow)
	if err != nil {
		t.Fatalf("a client refuses the release: %v", err)
	}
	target, ref, ok, err := verified.Newest("nightly", "arm64", autoupdate.Compare)
	if err != nil || !ok || ref.Version != "0.3.1" {
		t.Fatalf("newest = %+v ok=%v err=%v", ref, ok, err)
	}
	if err := target.Match(mustRead(t, arm)); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := archivetrust.ReadArchiveManifest(arm)
	if err != nil {
		t.Fatal(err)
	}
	// The signed targets name the hash of the archive's manifest, which setup holds
	// a machine's report to before the operator's wallet signs it.
	want := `{"version":"0.3.1","arch":"arm64","channel":"nightly","manifest_sha256":"` + archivetrust.ManifestDigest(manifest) + `"}`
	if string(target.Custom) != want {
		t.Fatalf("custom = %s, want %s", target.Custom, want)
	}
	pending, err := repo.ReadPending()
	if err != nil || pending == nil || pending.Tag != plan.Tag || len(pending.Assets) != 2 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}

func TestCut_everyRoleReEncodesToTheBytesTheAgentSigned(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, "main"), archive(t, "0.3.0", "amd64", "x"))); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, payload := range agent.payloads {
		if !bytes.Equal(canonicalAgain(t, payload), payload) {
			t.Fatalf("a payload is not canonical JSON: %s", payload)
		}
		var head struct {
			Type string `json:"_type"`
		}
		if err := json.Unmarshal(payload, &head); err != nil {
			t.Fatal(err)
		}
		seen[head.Type] = true
	}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		if !seen[role] {
			t.Errorf("no %s payload was signed", role)
		}
	}
}

func TestCut_aSecondVersionRaisesEveryVersionAndRetentionDropsTheOldest(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	ch := mustChannel(t, "nightly")
	for i, v := range []string{"0.3.1", "0.3.2", "0.3.3"} {
		p := cutParams(repo, agent, ch, archive(t, v, "amd64", "bytes "+v))
		p.Retention = 2
		plan, err := Cut(t.Context(), p)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		published(t, repo)
		if plan.TargetsVersion != int64(i+1) || plan.SnapshotVersion != int64(i+1) || plan.TimestampVersion != int64(i+1) {
			t.Fatalf("%s: versions %d/%d/%d", v, plan.TargetsVersion, plan.SnapshotVersion, plan.TimestampVersion)
		}
		if i == 2 && (len(plan.Dropped) != 1 || !strings.Contains(plan.Dropped[0], "0.3.1")) {
			t.Fatalf("dropped = %v, want 0.3.1", plan.Dropped)
		}
	}
	verified, err := clientView(t, repo, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, listed := verified.Targets[releaseverify.ArchiveTarget("nightly", "0.3.1", "amd64")]; listed || len(verified.Targets) != 2 {
		t.Fatalf("targets = %v", verified.Targets)
	}
}

func TestCut_channelsShareOneTargetsFileAndRetentionIsPerChannel(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	for _, c := range []struct{ channel, version string }{{"nightly", "0.3.1"}, {"main", "0.3.0"}, {"dev/my-branch", "0.3.2"}, {"nightly", "0.3.2"}} {
		p := cutParams(repo, agent, mustChannel(t, c.channel), archive(t, c.version, "amd64", c.channel+c.version))
		p.Retention = 1
		if _, err := Cut(t.Context(), p); err != nil {
			t.Fatalf("%s %s: %v", c.channel, c.version, err)
		}
		published(t, repo)
	}
	verified, err := clientView(t, repo, testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nightly/orama-0.3.2-linux-amd64.tar.gz", "main/orama-0.3.0-linux-amd64.tar.gz", "dev/my-branch/orama-0.3.2-linux-amd64.tar.gz"}
	if len(verified.Targets) != len(want) {
		t.Fatalf("targets = %v", verified.Targets)
	}
	for _, w := range want {
		if _, ok := verified.Targets[w]; !ok {
			t.Errorf("%s is not listed", w)
		}
	}
}

func TestCut_refusals(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	ch := mustChannel(t, "nightly")
	if _, err := Cut(t.Context(), cutParams(repo, agent, ch, archive(t, "0.3.5", "amd64", "five"))); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"an older version":               {archive(t, "0.3.4", "amd64", "four")},
		"the same bytes again":           {archive(t, "0.3.5", "amd64", "five")},
		"the same path, new bytes":       {archive(t, "0.3.5", "amd64", "FIVE")},
		"no archive":                     nil,
		"mixed versions":                 {archive(t, "0.3.6", "amd64", "a"), archive(t, "0.3.7", "arm64", "b")},
		"two for one arch":               {archive(t, "0.3.6", "amd64", "a"), archive(t, "0.3.6", "amd64", "b")},
		"a version with a plus":          {archive(t, "0.3.6+x", "amd64", "a")},
		"a version clients cannot order": {archive(t, "0.3.6-rc1", "amd64", "a")},
	}
	for name, archives := range cases {
		before := agent.approvals()
		if _, err := Cut(t.Context(), cutParams(repo, agent, ch, archives...)); err == nil {
			t.Errorf("%s was released", name)
		}
		if agent.approvals() != before {
			t.Errorf("%s: a person was asked to approve a release that was refused", name)
		}
	}
	bad := filepath.Join(t.TempDir(), "orama.tar.gz")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Cut(t.Context(), cutParams(repo, agent, ch, bad)); err == nil {
		t.Error("a file that is not named like a release archive was released")
	}
	if _, err := Cut(t.Context(), cutParams(repo, agent, ch, filepath.Join(t.TempDir(), "orama-0.3.6-linux-amd64.tar.gz"))); err == nil {
		t.Error("an archive that does not exist was released")
	}
}

func TestCut_replaceChangesTheBytesOfAListedPath(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	ch := mustChannel(t, "dev/my-branch")
	if _, err := Cut(t.Context(), cutParams(repo, agent, ch, archive(t, "0.3.5", "amd64", "one"))); err != nil {
		t.Fatal(err)
	}
	published(t, repo)
	two := archive(t, "0.3.5", "amd64", "two")
	p := cutParams(repo, agent, ch, two)
	p.Replace = true
	plan, err := Cut(t.Context(), p)
	if err != nil || len(plan.Replaced) != 1 {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
	verified, err := clientView(t, repo, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := verified.Targets["dev/my-branch/orama-0.3.5-linux-amd64.tar.gz"].Match(mustRead(t, two)); err != nil {
		t.Fatal(err)
	}
}

func TestCut_dryRunNeedsNoAgentAndWritesNothing(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	p := cutParams(repo, nil, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))
	p.DryRun = true

	plan, err := Cut(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetsVersion != 1 || len(plan.Added) != 1 || plan.Tag != "release-nightly-0.3.1" {
		t.Fatalf("plan = %+v", plan)
	}
	for _, name := range []string{TargetsFile, SnapshotFile, TimestampFile, PendingFile} {
		if _, err := os.Stat(filepath.Join(repo.Dir, name)); err == nil {
			t.Errorf("a dry run wrote %s", name)
		}
	}
}

func TestCut_aWalletThatIsNotTheRootsKeyIsRefusedBeforeAnyApproval(t *testing.T) {
	repo := newRepo(t, newFakeAgent(t))
	other := newFakeAgent(t)
	if _, err := Cut(t.Context(), cutParams(repo, other, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))); err == nil {
		t.Fatal("a release was signed by a key the root does not list")
	}
	if other.approvals() != 0 {
		t.Fatal("a person was asked to approve a release no client would accept")
	}
}

func TestCut_aDeniedApprovalWritesNothing(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	agent.refuseAt, agent.refuseErr = agent.approvals()+2, errors.New("denied")
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))); err == nil {
		t.Fatal("a release was cut after a denied approval")
	}
	for _, name := range []string{TargetsFile, SnapshotFile, TimestampFile, PendingFile} {
		if _, err := os.Stat(filepath.Join(repo.Dir, name)); err == nil {
			t.Errorf("%s was written after a denied approval", name)
		}
	}
}

func TestCut_anExpiredRootIsRefused(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	p := cutParams(repo, agent, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))
	p.Now = testNow.Add(RootValidity + time.Hour)
	if _, err := Cut(t.Context(), p); err == nil || !strings.Contains(err.Error(), "renew-root") {
		t.Fatalf("err = %v, want the expired root named with the way out", err)
	}
}

func TestCut_metadataNeverOutlivesTheRoot(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	p := cutParams(repo, agent, mustChannel(t, "main"), archive(t, "0.3.0", "amd64", "x"))
	p.Now = testNow.Add(RootValidity - 48*time.Hour)
	plan, err := Cut(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	_, root, _ := repo.ReadRoot()
	if plan.TargetsExpires.After(root.Signed.Expires) || plan.TimestampExpires.After(root.Signed.Expires) {
		t.Fatalf("expiries %s and %s outlive the root's %s", plan.TargetsExpires, plan.TimestampExpires, root.Signed.Expires)
	}
}

func TestRefreshTimestamp_isOneApprovalAndKeepsTheSnapshot(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))); err != nil {
		t.Fatal(err)
	}
	snapshot := mustRead(t, filepath.Join(repo.Dir, SnapshotFile))
	before := agent.approvals()
	later := testNow.Add(6 * 24 * time.Hour)

	version, err := RefreshTimestamp(t.Context(), agent, repo, mustChannel(t, "nightly"), later, nil)
	if err != nil || version != 2 {
		t.Fatalf("version %d, err %v", version, err)
	}
	if agent.approvals()-before != 1 {
		t.Fatalf("approvals = %d, want 1", agent.approvals()-before)
	}
	if !bytes.Equal(snapshot, mustRead(t, filepath.Join(repo.Dir, SnapshotFile))) {
		t.Fatal("a refresh changed the snapshot")
	}
	if _, err := clientView(t, repo, later.Add(6*24*time.Hour)); err != nil {
		t.Fatalf("a client refuses a refreshed timestamp: %v", err)
	}
	if _, err := clientView(t, repo, testNow.Add(8*24*time.Hour)); err != nil {
		t.Fatalf("the refresh did not extend the repository's life: %v", err)
	}
	ts, _ := metadata.Timestamp().FromFile(filepath.Join(repo.Dir, TimestampFile))
	if want := later.Add(ShortTimestampValidity); !ts.Signed.Expires.Equal(want) {
		t.Fatalf("expires %s, want %s", ts.Signed.Expires, want)
	}
}

func TestRefreshTimestamp_mainGetsThirtyDaysAndAFrozenOneIsAFreeze(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, "main"), archive(t, "0.3.0", "amd64", "x"))); err != nil {
		t.Fatal(err)
	}
	ts, _ := metadata.Timestamp().FromFile(filepath.Join(repo.Dir, TimestampFile))
	if want := testNow.Add(MainTimestampValidity); !ts.Signed.Expires.Equal(want) {
		t.Fatalf("main timestamp expires %s, want %s", ts.Signed.Expires, want)
	}
	if _, err := clientView(t, repo, testNow.Add(MainTimestampValidity+time.Hour)); !errors.Is(err, releaseverify.ErrFreeze) {
		t.Fatalf("err = %v, want a freeze", err)
	}
}

func TestRefreshTimestamp_needsAReleaseAndTheRootsWallet(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if _, err := RefreshTimestamp(t.Context(), agent, repo, mustChannel(t, "nightly"), testNow, nil); err == nil {
		t.Fatal("a timestamp was refreshed in a repository with no release")
	}
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshTimestamp(t.Context(), newFakeAgent(t), repo, mustChannel(t, "nightly"), testNow, nil); err == nil {
		t.Fatal("another wallet refreshed the timestamp")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCut_aSecondCutBeforeThePublishIsRefused(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	ch := mustChannel(t, "nightly")
	if _, err := Cut(t.Context(), cutParams(repo, agent, ch, archive(t, "0.3.1", "amd64", "one"))); err != nil {
		t.Fatal(err)
	}
	before := agent.approvals()
	_, err := Cut(t.Context(), cutParams(repo, agent, ch, archive(t, "0.3.2", "amd64", "two")))
	if err == nil || !strings.Contains(err.Error(), "never published") {
		t.Fatalf("err = %v", err)
	}
	if agent.approvals() != before {
		t.Fatal("a person was asked to approve a cut that would orphan the first")
	}
}

func TestCut_replaceIsForDevChannelsOnly(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	for _, channel := range []string{"nightly", "main"} {
		p := cutParams(repo, agent, mustChannel(t, channel), archive(t, "0.3.1", "amd64", "x"))
		p.Replace = true
		if _, err := Cut(t.Context(), p); err == nil || !strings.Contains(err.Error(), "dev/<branch>") {
			t.Errorf("%s: err = %v", channel, err)
		}
	}
}

func TestInitRoot_aRootThatCannotBeReadIsNotOverwritten(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if err := os.WriteFile(filepath.Join(repo.Dir, RootFile), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := agent.approvals()
	if _, err := InitRoot(t.Context(), agent, repo, testNow, nil); err == nil {
		t.Fatal("a corrupt root was replaced by a new version 1")
	}
	if agent.approvals() != before {
		t.Fatal("a person was asked to approve a root over a corrupt one")
	}
}

// published stands for a successful publish: the pending record is gone.
func published(t *testing.T, repo Repo) {
	t.Helper()
	if err := repo.clearPending(); err != nil {
		t.Fatal(err)
	}
}
