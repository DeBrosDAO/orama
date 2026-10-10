package setup

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// releaseIPs are n machines, 203.0.113.101 and up.
func releaseIPs(n int) []string {
	ips := make([]string, n)
	for i := range ips {
		ips[i] = fmt.Sprintf("203.0.113.%d", 101+i)
	}
	return ips
}

// clusterOnly is a run that stops at the cluster node: the release step is what
// these tests are about.
func clusterOnly(h *harness, ips ...string) Options {
	opts := h.opts(ips...)
	opts.ClusterOnly, opts.StorageGB, opts.Name = true, 0, ""
	return opts
}

func TestRelease_everyMachineDownloadsItAtOnceAndTheLaptopAcceptsTheMatchingHashes(t *testing.T) {
	h := newHarness()
	ips := releaseIPs(5)
	h.enroll.gate = newFetchGate(len(ips))
	mustRun(t, h, clusterOnly(h, ips...))

	if got := h.enroll.gate.maxSeen(); got != len(ips) {
		t.Errorf("%d machines were downloading at once, want all %d: they are not fetched in parallel", got, len(ips))
	}
	for _, ip := range ips {
		if h.w.index("download "+ip+" "+testReleaseURL) < 0 {
			t.Errorf("%s did not download the archive from the repository:\n%s", ip, strings.Join(h.w.entries(), "\n"))
		}
		if h.w.index("stage "+ip) < 0 || !h.report.has(ip, StepRelease, StateDone) {
			t.Errorf("%s did not stage the release", ip)
		}
	}
	if h.w.count("endorse ") != 1 {
		t.Errorf("the operator's wallet signs the release once, not once per machine; got %d", h.w.count("endorse "))
	}
	if h.w.index("fetch release") >= 0 {
		t.Error("this computer downloaded the archive although the machines do")
	}
	last := -1
	for _, ip := range ips {
		last = max(last, h.w.index("stage "+ip))
	}
	if h.w.count("accept release") != 1 || h.w.index("accept release") < last {
		t.Errorf("the rollback record is raised once, after every machine confirmed the archive:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("remove release metadata") != 1 {
		t.Error("the metadata fetched here is removed")
	}
}

func TestRelease_atMostMaxReleaseFetchesMachinesDownloadAtOnce(t *testing.T) {
	h := newHarness()
	ips := releaseIPs(maxReleaseFetches + 3)
	h.enroll.gate = newFetchGate(maxReleaseFetches)
	mustRun(t, h, clusterOnly(h, ips...))
	if got := h.enroll.gate.maxSeen(); got != maxReleaseFetches {
		t.Errorf("%d machines downloaded at once, want the limit %d", got, maxReleaseFetches)
	}
	for _, ip := range ips {
		if h.w.index("stage "+ip) < 0 {
			t.Errorf("%s was not given the release", ip)
		}
	}
}

func TestRelease_aMachineThatReportsAnotherArchiveIsRefused(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip2: {downloadSHA: strings.Repeat("0", 64)}}
	_, err := run(t, h, clusterOnly(h, ip1, ip2, ip3))
	if err == nil || !strings.Contains(err.Error(), ip2) || !strings.Contains(err.Error(), strings.Repeat("0", 64)) || !strings.Contains(err.Error(), testArchiveSHA) {
		t.Fatalf("got %v, want the machine and both digests named", err)
	}
	if h.w.index("stage "+ip2) >= 0 {
		t.Error("an archive other than the signed one was staged")
	}
	if h.w.index("discard "+ip2) < 0 {
		t.Error("the refused download stays on the machine")
	}
	if h.w.index("accept release") >= 0 || h.w.index("cluster ") >= 0 {
		t.Errorf("the run went on after a machine reported another archive:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if !h.report.has(ip2, StepRelease, StateFailed) {
		t.Error("the refusal is not reported on the machine")
	}
}

func TestRelease_aMachineThatCannotReachTheRepositoryFailsWithTheURLAndIsNotGivenAnUpload(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip2: {fetchErr: errRepositoryUnreachable}}
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil {
		t.Fatal("a machine that could not download the release was passed")
	}
	for _, want := range []string{ip2, testReleaseURL, "Could not resolve host", "--upload-release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
	if h.w.index("fetch release") >= 0 || h.w.index("stage "+ip2) >= 0 {
		t.Errorf("setup uploaded the archive by itself:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("cluster ") >= 0 || h.w.index("accept release") >= 0 {
		t.Error("the run went on after a machine failed to download")
	}
}

func TestRelease_aMachineThatAlreadyRunsTheReleaseIsSkippedAndItsDownloadRemoved(t *testing.T) {
	h := newHarness()
	f := installedFacts()
	f.GlobalInstalled, f.ChainActive = false, false
	h.enroll.facts = map[string]Facts{ip1: f}
	mustRun(t, h, h.opts(ip1, ip2))
	if h.w.index("stage "+ip1) >= 0 {
		t.Errorf("a machine that runs exactly this build was given it again:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("stage "+ip2) < 0 {
		t.Error("the fresh machine was not given the release")
	}
	if !h.report.has(ip1, StepRelease, StateSkipped) {
		t.Error("the skip is not reported")
	}
	if h.w.index("discard "+ip1) < 0 {
		t.Error("the download of a machine that did not need it stays on it")
	}
	if h.w.index("accept release") < 0 {
		t.Error("the release was checked by the machines, and the rollback record was not raised")
	}
}

func TestRelease_aMachineThatEndsUpWithAnotherBuildIsRefused(t *testing.T) {
	h := newHarness()
	other := strings.Repeat("9", 64)
	h.enroll.releases = map[string]fakeRelease{ip1: {stagedManifest: other}}
	_, err := run(t, h, clusterOnly(h, ip1))
	if err == nil || !strings.Contains(err.Error(), ip1) || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), testManifest) {
		t.Fatalf("got %v, want the machine and both manifests named", err)
	}
	if h.w.index("accept release") >= 0 || h.w.index("cluster ") >= 0 {
		t.Error("the run went on with a machine that runs another build than the endorsed one")
	}
}

// liarManifest is a manifest that is for the right release and lists checksums of
// the machine's own choosing.
var liarManifest = []byte(`{"version":"0.3.1","arch":"amd64","checksums":{"orama":"` + strings.Repeat("0", 64) + `"}}`)

func TestRelease_aMachineThatReportsAManifestOtherThanTheSignedOneNeverGetsASignature(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip1: {manifest: liarManifest}}
	_, err := run(t, h, clusterOnly(h, ip1))
	if err == nil || !strings.Contains(err.Error(), ip1) || !strings.Contains(err.Error(), "signed metadata names") {
		t.Fatalf("got %v, want the machine named with the digest the metadata gives", err)
	}
	if h.w.count("endorse ") != 0 {
		t.Errorf("the operator's wallet was asked to sign a manifest the signed metadata does not name:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("stage ") >= 0 || h.w.index("accept release") >= 0 {
		t.Error("the run went on with a manifest the signed metadata does not name")
	}
	if h.w.index("discard "+ip1) < 0 {
		t.Error("the download of the refused machine stays on it")
	}
}

// Every machine is held to the digest, not only the first one to ask: whichever
// order they arrive in, the liar is the one refused and named.
func TestRelease_everyMachineIsHeldToTheSignedManifestDigest(t *testing.T) {
	for _, liar := range []string{ip1, ip2, ip3} {
		h := newHarness()
		h.enroll.gate = newFetchGate(3)
		h.enroll.releases = map[string]fakeRelease{liar: {manifest: liarManifest}}
		_, err := run(t, h, clusterOnly(h, ip1, ip2, ip3))
		if err == nil || !strings.Contains(err.Error(), "machine "+liar+":") || !strings.Contains(err.Error(), "signed metadata names") {
			t.Fatalf("liar %s: got %v", liar, err)
		}
		if h.w.index("stage "+liar) >= 0 {
			t.Errorf("liar %s was given the release", liar)
		}
		if h.w.count("endorse ") > 1 {
			t.Errorf("liar %s: the wallet was asked %d times", liar, h.w.count("endorse "))
		}
	}
}

func TestRelease_aRefusedSignatureIsAskedOnlyOnce(t *testing.T) {
	h := newHarness()
	h.deps.Releases = fakeReleases{w: h.w, endorseErr: errors.New("the RootWallet agent refused")}
	h.enroll.gate = newFetchGate(3)
	_, err := run(t, h, clusterOnly(h, ip1, ip2, ip3))
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("got %v", err)
	}
	if got := h.w.count("endorse "); got != 1 {
		t.Errorf("the wallet was asked %d times; a refusal is remembered", got)
	}
}

func TestRelease_aStageThatHasBegunFinishesWhenAnotherMachineFails(t *testing.T) {
	h := newHarness()
	entered, hold := make(chan struct{}), make(chan struct{})
	h.enroll.releases = map[string]fakeRelease{
		ip1: {stageEntered: entered, stageHold: hold},
		ip2: {fetchWaitFor: entered, fetchErr: errors.New("curl: (22) The requested URL returned error: 404")},
	}
	go func() {
		// Release the stage once the other machine's failure has been reported.
		for !h.report.has(ip2, StepRelease, StateFailed) {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
		close(hold)
	}()
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), ip2) {
		t.Fatalf("got %v, want the machine that failed", err)
	}
	if h.w.index("stage-finished "+ip1+" ctx-ended=false") < 0 {
		t.Errorf("the stage in progress was cut short or never finished:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("discard "+ip1) >= 0 {
		t.Errorf("the directory of a stage that had begun was removed from under it:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if !h.report.has(ip1, StepRelease, StateDone) {
		t.Error("the machine that was staging did not finish")
	}
	if h.w.index("accept release") >= 0 {
		t.Error("the rollback record was raised although a machine failed")
	}
}

func TestRelease_aMachineStoppedByAnotherFailureIsReportedAsCancelled(t *testing.T) {
	h := newHarness()
	h.enroll.gate = newFetchGate(2)
	h.enroll.releases = map[string]fakeRelease{ip1: {downloadSHA: strings.Repeat("0", 64)}, ip2: {fetchUntilStopped: true}}
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "machine "+ip1+":") || strings.Contains(err.Error(), ip2) {
		t.Fatalf("got %v, want only the machine that failed", err)
	}
	if got := h.report.detail(ip2, StepRelease, StateFailed); got != "cancelled: "+ip1+" failed" {
		t.Errorf("the stopped machine reports %q", got)
	}
}

func TestRelease_theUploadHintIsForARepositoryTheMachineCannotReachOnly(t *testing.T) {
	cases := map[string]struct {
		release fakeRelease
		hint    bool
	}{
		"a name that does not resolve":   {fakeRelease{fetchErr: errors.New("curl: (6) Could not resolve host: releases.example")}, true},
		"a refused connection":           {fakeRelease{fetchErr: errors.New("curl: (7) Failed to connect to releases.example port 443")}, true},
		"a certificate that fails":       {fakeRelease{fetchErr: errors.New("curl: (60) SSL certificate problem")}, true},
		"a 404":                          {fakeRelease{fetchErr: errors.New("curl: (22) The requested URL returned error: 404")}, false},
		"a download of the wrong size":   {fakeRelease{fetchErr: errors.New("the download is 7 bytes, the signed metadata says 13")}, false},
		"a download of another digest":   {fakeRelease{fetchErr: errors.New("the download has sha256 00, the signed metadata says 11")}, false},
		"a filesystem without room":      {fakeRelease{fetchErr: errors.New("the filesystem of /var/tmp/orama-archive.AbC12345 has 100 MiB free")}, false},
		"a machine without curl":         {fakeRelease{fetchErr: errors.New("this machine has no curl; install it (apt-get install curl) and run setup again")}, false},
		"a digest the machine reports":   {fakeRelease{downloadSHA: strings.Repeat("0", 64)}, false},
		"a manifest the machine invents": {fakeRelease{manifest: liarManifest}, false},
	}
	for name, c := range cases {
		h := newHarness()
		h.enroll.releases = map[string]fakeRelease{ip1: c.release}
		_, err := run(t, h, clusterOnly(h, ip1))
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if got := strings.Contains(err.Error(), "--upload-release"); got != c.hint {
			t.Errorf("%s: hint = %v, want %v: %v", name, got, c.hint, err)
		}
	}
}

func TestRelease_aRefusedSignatureStagesNothingAndRemovesTheDownloads(t *testing.T) {
	h := newHarness()
	h.deps.Releases = fakeReleases{w: h.w, endorseErr: errors.New("the RootWallet agent is locked")}
	// Both machines are downloading before either hears of the refusal.
	h.enroll.gate = newFetchGate(2)
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("stage ") >= 0 || h.w.index("accept release") >= 0 {
		t.Errorf("a release the operator did not sign was staged:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("discard ") != 2 {
		t.Errorf("both downloads are removed, got %d", h.w.count("discard "))
	}
}

func TestRelease_aStageThatFailsOnTheMachineIsReportedAndItsDirectoryLeftToTheStage(t *testing.T) {
	h := newHarness()
	h.enroll.releases = map[string]fakeRelease{ip1: {stageErr: errors.New("manifest.sig does not recover to a trusted signer")}}
	_, err := run(t, h, clusterOnly(h, ip1))
	if err == nil || !strings.Contains(err.Error(), ip1) || !strings.Contains(err.Error(), "trusted signer") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("discard "+ip1) >= 0 {
		t.Error("the directory of a stage that began was removed from outside: the stage command removes it")
	}
}

func TestRelease_aReleaseThatCannotBeResolvedTouchesNoMachine(t *testing.T) {
	h := newHarness()
	h.deps.Releases = fakeReleases{w: h.w, resolveErr: errors.New("the nightly channel lists no release for linux/amd64")}
	_, err := run(t, h, clusterOnly(h, ip1, ip2))
	if err == nil || !strings.Contains(err.Error(), "no release for linux/amd64") {
		t.Fatalf("got %v", err)
	}
	if h.w.index("download ") >= 0 || h.w.index("stage ") >= 0 {
		t.Error("a machine was told to download a release that was not verified")
	}
}

func TestRelease_uploadReleaseKeepsTheLaptopDownloadingAndUploading(t *testing.T) {
	h := newHarness()
	opts := clusterOnly(h, ip1, ip2)
	opts.UploadRelease = true
	mustRun(t, h, opts)
	if h.w.count("fetch release") != 1 || h.w.index("stage "+ip1) < 0 || h.w.index("stage "+ip2) < 0 {
		t.Errorf("the archive is downloaded here once and staged on each machine:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("resolve release") >= 0 || h.w.index("download ") >= 0 {
		t.Error("a machine downloaded the release although --upload-release was given")
	}
}

func TestRelease_machinesOfTwoArchitecturesGetTheirOwnRelease(t *testing.T) {
	h := newHarness()
	arm := freshFacts()
	arm.Arch = "arm64"
	h.enroll.facts = map[string]Facts{ip2: arm}
	mustRun(t, h, clusterOnly(h, ip1, ip2))
	if h.w.index("resolve release amd64") < 0 || h.w.index("resolve release arm64") < 0 {
		t.Errorf("each architecture is resolved:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.count("accept release") != 2 {
		t.Errorf("each architecture's release is accepted, got %d", h.w.count("accept release"))
	}
}

func TestCommandLine_carriesUploadRelease(t *testing.T) {
	o := Options{Network: "stagenet", IPs: []string{ip1}, Name: "alice", UploadRelease: true, StorageGB: DefaultStorageGB}
	if line := o.CommandLine(); !strings.Contains(line, " --upload-release") {
		t.Errorf("a run that was told to upload would download on the next try: %s", line)
	}
	o.UploadRelease = false
	if line := o.CommandLine(); strings.Contains(line, "upload-release") {
		t.Errorf("a run that downloads names --upload-release: %s", line)
	}
}
