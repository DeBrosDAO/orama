package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

const (
	testReleaseVersion = "0.3.1"
	testReleaseURL     = "https://releases.example/targets/nightly/orama-0.3.1-linux-amd64.tar.gz"
	testArchiveSHA     = "ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55"
	testArchiveDir     = "/tmp/orama-archive.AbC12345"
)

// testReleaseManifest is the manifest every fake machine reports out of its archive.
var testReleaseManifest = []byte(`{"version":"` + testReleaseVersion + `","arch":"amd64"}`)

// fakeReleases is the release repository and the operator's wallet.
type fakeReleases struct {
	w *world
	// resolveErr and endorseErr are what Resolve and Endorse fail with.
	resolveErr, endorseErr error
}

func (f fakeReleases) Fetch(_ context.Context, _ *netregistry.Network, arch string) (*Release, error) {
	f.w.add("fetch release %s", arch)
	return &Release{Version: testReleaseVersion, Arch: arch, ManifestSHA256: testManifest, CLISHA256: testCLISHA}, nil
}

func (f fakeReleases) Resolve(_ context.Context, _ *netregistry.Network, arch string) (*ReleaseRef, error) {
	f.w.add("resolve release %s", arch)
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return &ReleaseRef{
		Version: testReleaseVersion, Arch: arch, URL: testReleaseURL, SHA256: testArchiveSHA, Length: 312 << 20, Root: []byte("release-root"),
		Accept: func() error { f.w.add("accept release %s", arch); return nil },
		Remove: func() error { f.w.add("remove release metadata %s", arch); return nil },
	}, nil
}

func (f fakeReleases) Endorse(_ context.Context, ref *ReleaseRef, manifest []byte) (*Endorsement, error) {
	f.w.add("endorse %s", ref.Version)
	if f.endorseErr != nil {
		return nil, f.endorseErr
	}
	return &Endorsement{Manifest: append([]byte("sealed:"), manifest...), Signature: "0xsig", ManifestSHA256: testManifest, CLISHA256: testCLISHA}, nil
}

// fakeRelease is how one fake machine behaves when it fetches the release itself.
type fakeRelease struct {
	// fetchErr is what the download fails with (the repository cannot be reached).
	fetchErr error
	// downloadSHA is the digest the machine reports; empty reports the signed one.
	downloadSHA string
	// manifest is what the machine reports out of the archive; nil is the release's.
	manifest []byte
	// stageErr fails the staging; stagedManifest is the manifest the machine ends
	// up running when it is not the endorsed one.
	stageErr       error
	stagedManifest string
}

// fetchGate holds every machine's download until want of them are downloading at
// once, and records the most that were: the proof that the machines fetch in
// parallel, and no more than the limit.
type fetchGate struct {
	want    int
	mu      sync.Mutex
	now     int
	max     int
	once    sync.Once
	reached chan struct{}
}

func newFetchGate(want int) *fetchGate { return &fetchGate{want: want, reached: make(chan struct{})} }

// enter blocks until want downloads are in flight, or fails after a second.
func (g *fetchGate) enter() error {
	g.mu.Lock()
	g.now++
	g.max = max(g.max, g.now)
	if g.now >= g.want {
		g.once.Do(func() { close(g.reached) })
	}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.now--
		g.mu.Unlock()
	}()
	select {
	case <-g.reached:
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("only %d downloads were in flight at once, the gate waits for %d", g.maxSeen(), g.want)
	}
}

func (g *fetchGate) maxSeen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

func (m *fakeMachine) FetchRelease(_ context.Context, ref *ReleaseRef) (*FetchedRelease, error) {
	m.w.add("download %s %s", m.ip, ref.URL)
	if m.release.fetchErr != nil {
		return nil, m.release.fetchErr
	}
	if m.gate != nil {
		if err := m.gate.enter(); err != nil {
			return nil, err
		}
	}
	f := &FetchedRelease{Dir: testArchiveDir, SHA256: ref.SHA256, Manifest: testReleaseManifest}
	if m.release.downloadSHA != "" {
		f.SHA256 = m.release.downloadSHA
	}
	if m.release.manifest != nil {
		f.Manifest = m.release.manifest
	}
	return f, nil
}

func (m *fakeMachine) StageFetched(_ context.Context, f *FetchedRelease, e *Endorsement) error {
	m.w.add("stage %s %s", m.ip, strings.TrimPrefix(string(e.Manifest), "sealed:"))
	if m.release.stageErr != nil {
		return m.release.stageErr
	}
	m.facts.ManifestSHA256, m.facts.CLISHA256 = e.ManifestSHA256, e.CLISHA256
	if m.release.stagedManifest != "" {
		m.facts.ManifestSHA256 = m.release.stagedManifest
	}
	return nil
}

func (m *fakeMachine) DiscardFetched(_ context.Context, f *FetchedRelease) error {
	m.w.add("discard %s %s", m.ip, f.Dir)
	return nil
}

var errRepositoryUnreachable = errors.New("curl: (6) Could not resolve host: releases.example")
