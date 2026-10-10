package releasepub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// recorder is a Runner that remembers every command and answers `gh release
// view` as the test says.
type recorder struct {
	calls     []string
	viewError string
	failOn    string
	// assets is what `gh release view --json assets` lists.
	assets string
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	switch {
	case r.failOn != "" && strings.HasPrefix(line, r.failOn):
		return []byte("boom"), errors.New("exit status 1")
	case strings.HasPrefix(line, "gh release view") && r.viewError != "":
		return []byte(r.viewError), errors.New("exit status 1")
	case strings.HasPrefix(line, "gh release view"):
		if r.assets == "" {
			return []byte(`{"assets":[]}`), nil
		}
		return []byte(r.assets), nil
	}
	return nil, nil
}

func cutOne(t *testing.T, repo Repo, agent *fakeAgent, channel, version, content string) string {
	t.Helper()
	path := archive(t, version, "amd64", content)
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, channel), path)); err != nil {
		t.Fatal(err)
	}
	return path
}

func publishParams(repo Repo, r *recorder) PublishParams {
	return PublishParams{Repo: repo, GitHubRepo: "DeBrosDAO/orama", MetadataDest: "releases.example.org:/srv/releases/", Now: testNow, Run: r.run}
}

func TestPublish_uploadsArchivesFirstThenMetadataWithTheTimestampLast(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	path := cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	rec := &recorder{}

	if err := Publish(t.Context(), publishParams(repo, rec)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"gh release view release-nightly-0.3.1 --repo DeBrosDAO/orama --json assets",
		"gh release upload release-nightly-0.3.1 --repo DeBrosDAO/orama " + path,
	}
	if len(rec.calls) != 4 || rec.calls[0] != want[0] || rec.calls[1] != want[1] {
		t.Fatalf("calls = %q", rec.calls)
	}
	first, last := rec.calls[2], rec.calls[3]
	for _, f := range []string{"1.root.json", "root.json", "targets.json", "snapshot.json"} {
		if !strings.Contains(first, "/"+f) {
			t.Errorf("the first rsync lacks %s: %s", f, first)
		}
	}
	if strings.Contains(first, "timestamp.json") || !strings.HasPrefix(last, "rsync") || !strings.Contains(last, "/timestamp.json releases.example.org:/srv/releases/") {
		t.Fatalf("the timestamp is not alone and last:\n%s\n%s", first, last)
	}
	if strings.Contains(first, PendingFile) || strings.Contains(last, PendingFile) {
		t.Fatal("the pending record was sent to the host")
	}
	if p, _ := repo.ReadPending(); p != nil {
		t.Fatal("the pending record outlived the upload")
	}
}

func TestPublish_createsTheGitHubReleaseWhenThereIsNone(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	rec := &recorder{viewError: "release not found"}

	if err := Publish(t.Context(), publishParams(repo, rec)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.calls[1], "gh release create release-nightly-0.3.1 --repo DeBrosDAO/orama --title nightly 0.3.1") || !strings.HasSuffix(rec.calls[1], "--prerelease") {
		t.Fatalf("create = %q", rec.calls[1])
	}
}

func TestPublish_aMainReleaseIsNotAPrerelease(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "main", "0.3.0", "bytes")
	rec := &recorder{viewError: "release not found"}
	if err := Publish(t.Context(), publishParams(repo, rec)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.calls[1], "--prerelease") {
		t.Fatalf("create = %q", rec.calls[1])
	}
}

func TestPublish_anyOtherGhFailureStopsBeforeAnythingIsSent(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	rec := &recorder{viewError: "HTTP 401: bad credentials"}
	if err := Publish(t.Context(), publishParams(repo, rec)); err == nil {
		t.Fatal("a failed GitHub lookup was published through")
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "rsync") {
			t.Fatal("metadata was sent although the archives were not uploaded")
		}
	}
	if p, _ := repo.ReadPending(); p == nil {
		t.Fatal("the pending record was cleared by a failed publish")
	}
}

func TestPublish_aFailedUploadOrRsyncIsAnError(t *testing.T) {
	for _, failOn := range []string{"gh release upload", "rsync"} {
		agent := newFakeAgent(t)
		repo := newRepo(t, agent)
		cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
		if err := Publish(t.Context(), publishParams(repo, &recorder{failOn: failOn})); err == nil {
			t.Errorf("a failing %q was ignored", failOn)
		}
		if p, _ := repo.ReadPending(); p == nil {
			t.Errorf("%s: the pending record was cleared by a failed publish", failOn)
		}
	}
}

func TestPublish_anArchiveChangedAfterTheCutIsNotUploaded(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	path := cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	if err := os.WriteFile(path, []byte("swapped"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if err := Publish(t.Context(), publishParams(repo, rec)); err == nil || !strings.Contains(err.Error(), "changed after the cut") {
		t.Fatalf("err = %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("commands ran: %q", rec.calls)
	}
}

func TestPublish_metadataThatWouldNotVerifyIsNotSent(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	if err := os.WriteFile(filepath.Join(repo.Dir, TargetsFile), []byte(`{"signed":{},"signatures":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if err := Publish(t.Context(), publishParams(repo, rec)); err == nil || len(rec.calls) != 0 {
		t.Fatalf("err = %v, calls = %q", err, rec.calls)
	}
	p := publishParams(repo, rec)
	p.Now = testNow.Add(30 * 24 * time.Hour)
	if err := Publish(t.Context(), p); err == nil {
		t.Fatal("an expired timestamp was published")
	}
}

func TestPublish_aRefreshUploadsMetadataOnly(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	if err := Publish(t.Context(), publishParams(repo, &recorder{})); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshTimestamp(t.Context(), agent, repo, mustChannel(t, "nightly"), testNow.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if err := Publish(t.Context(), publishParams(repo, rec)); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "gh") {
			t.Fatalf("a refresh touched GitHub: %q", rec.calls)
		}
	}
}

func TestPublish_dryRunPrintsAndRunsNothing(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	var out bytes.Buffer
	p := publishParams(repo, &recorder{})
	p.DryRun, p.Progress, p.Run = true, &out, nil

	if err := Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gh release upload release-nightly-0.3.1", "rsync -a", "timestamp.json releases.example.org"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
	if pending, _ := repo.ReadPending(); pending == nil {
		t.Fatal("a dry run cleared the pending record")
	}
}

// What publish sends, served as the release host serves it, is what the
// clients' fetch path (root rotation, then the channel's metadata, then the
// archive) accepts: the whole chain from a cut to an install.
func TestRelease_whatIsPublishedIsWhatAClientFetchesAndVerifies(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	archivePath := cutOne(t, repo, agent, "nightly", "0.3.1", "the nightly archive")
	content := string(mustRead(t, archivePath))
	if _, err := RenewRoot(t.Context(), agent, repo, testNow.Add(24*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	root1 := mustRead(t, filepath.Join(repo.Dir, "1.root.json"))

	served := t.TempDir()
	copyDir(t, repo.Dir, served, TargetsFile, SnapshotFile, TimestampFile, RootFile, "1.root.json", "2.root.json")
	servedArchive := filepath.Join(served, "targets", "nightly", "orama-0.3.1-linux-amd64.tar.gz")
	if err := os.MkdirAll(filepath.Dir(servedArchive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servedArchive, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	releaseverify.AllowLocalRepositories(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(served)))
	t.Cleanup(srv.Close)

	// The node holds the first root; the repository has since renewed it.
	node := t.TempDir()
	rootPath := filepath.Join(node, "release-root.json")
	if err := os.WriteFile(rootPath, root1, 0o644); err != nil {
		t.Fatal(err)
	}
	src := autoupdate.Source{RootPath: rootPath, SeenPath: filepath.Join(node, "seen.json"), WorkDir: filepath.Join(node, "work"), Arch: "amd64", Now: func() time.Time { return testNow.Add(time.Hour) }}
	rel, ok, err := src.Newest(t.Context(), srv.URL, "nightly")
	if err != nil || !ok {
		t.Fatalf("Newest: ok=%v err=%v", ok, err)
	}
	if err := src.Download(t.Context(), srv.URL, rel); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got := mustRead(t, rel.ArchivePath())
	if string(got) != content || rel.Version != "0.3.1" {
		t.Fatalf("version %s, archive %q", rel.Version, got)
	}
	if !bytes.Equal(mustRead(t, rootPath), mustRead(t, filepath.Join(repo.Dir, "2.root.json"))) {
		t.Fatal("the node did not follow the root renewal")
	}
}

func copyDir(t *testing.T, from, to string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(to, n), mustRead(t, filepath.Join(from, n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublish_aPublishThatStoppedAfterTheUploadCanBeRunAgain(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	path := cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{assets: fmt.Sprintf(`{"assets":[{"name":%q,"size":%d,"digest":""}]}`, filepath.Base(path), info.Size())}

	if err := Publish(t.Context(), publishParams(repo, rec)); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "gh release upload") {
			t.Fatalf("an archive the release already holds was uploaded again: %q", rec.calls)
		}
	}
	if !strings.HasPrefix(rec.calls[len(rec.calls)-1], "rsync") {
		t.Fatalf("the metadata was not sent: %q", rec.calls)
	}
}

func TestPublish_anArchiveOfThatNameWithOtherBytesIsNeverOverwritten(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	path := cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	name := filepath.Base(path)
	for label, assets := range map[string]string{
		"another size":   fmt.Sprintf(`{"assets":[{"name":%q,"size":1}]}`, name),
		"another digest": fmt.Sprintf(`{"assets":[{"name":%q,"size":%d,"digest":"sha256:%s"}]}`, name, mustStat(t, path), strings.Repeat("0", 64)),
	} {
		rec := &recorder{assets: assets}
		if err := Publish(t.Context(), publishParams(repo, rec)); err == nil || !strings.Contains(err.Error(), "never overwritten") {
			t.Errorf("%s: err = %v", label, err)
		}
		for _, c := range rec.calls {
			if strings.HasPrefix(c, "rsync") || strings.HasPrefix(c, "gh release upload") {
				t.Errorf("%s: %q ran", label, c)
			}
		}
	}
}

func TestPublish_aTamperedPendingRecordOrAnOptionLookingDestinationIsRefused(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	cutOne(t, repo, agent, "nightly", "0.3.1", "bytes")
	good := mustRead(t, filepath.Join(repo.Dir, PendingFile))
	cases := map[string]func(p *PublishParams) string{
		"a tag that is not the cut's": func(*PublishParams) string {
			return strings.Replace(string(good), "release-nightly-0.3.1", "--repo=evil/x", 1)
		},
		"a relative asset path": func(*PublishParams) string {
			return strings.Replace(string(good), `"path": "/`, `"path": "-x/`, 1)
		},
		"a destination that is an option": func(p *PublishParams) string { p.MetadataDest = "-e"; return string(good) },
		"a repository that is an option":  func(p *PublishParams) string { p.GitHubRepo = "--help"; return string(good) },
	}
	for name, change := range cases {
		p := publishParams(repo, &recorder{})
		body := change(&p)
		if err := os.WriteFile(filepath.Join(repo.Dir, PendingFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := &recorder{}
		p.Run = rec.run
		if err := Publish(t.Context(), p); err == nil {
			t.Errorf("%s was published", name)
		}
		if len(rec.calls) != 0 {
			t.Errorf("%s: commands ran: %q", name, rec.calls)
		}
	}
}

func mustStat(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
